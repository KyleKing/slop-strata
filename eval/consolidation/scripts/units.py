"""Tree-sitter unit extraction: functions/methods/consts with own-token lists. No regex on source."""
import sys
sys.setrecursionlimit(10000)
import tree_sitter as ts
import tree_sitter_python as tsp
import tree_sitter_typescript as tst
import tree_sitter_go as tsg

LANGS = {
    'py': ts.Language(tsp.language()),
    'ts': ts.Language(tst.language_typescript()),
    'tsx': ts.Language(tst.language_tsx()),
    'go': ts.Language(tsg.language()),
}
_parsers = {k: ts.Parser(v) for k, v in LANGS.items()}
EXT = {'.py': 'py', '.ts': 'ts', '.mts': 'ts', '.cts': 'ts', '.js': 'ts', '.mjs': 'ts', '.cjs': 'ts',
       '.tsx': 'tsx', '.jsx': 'tsx', '.go': 'go'}
REPO_LANG = {'py': 'py', 'ts': 'ts', 'tsx': 'ts', 'go': 'go'}

IDENT = {'identifier', 'property_identifier', 'shorthand_property_identifier', 'type_identifier',
         'field_identifier', 'package_identifier', 'shorthand_property_identifier_pattern',
         'statement_identifier', 'label_name', 'private_property_identifier'}
LIT_ATOM = {'string', 'template_string', 'number', 'regex', 'integer', 'float', 'interpreted_string_literal',
            'raw_string_literal', 'int_literal', 'float_literal', 'rune_literal', 'imaginary_literal',
            'concatenated_string'}
BLOCKS = {'block', 'module', 'statement_block', 'source_file', 'class_body', 'switch_body',
          'expression_list', 'declaration_list', 'program'}

class Unit:
    __slots__ = ('path', 'qual', 'name', 'kind', 'parent', 'sl', 'el', 'toks', 'norm', 'lines', 'stmts',
                 'bases', 'text')

def lang_for(path):
    i = path.rfind('.')
    if i < 0: return None
    return EXT.get(path[i:])

def _name_of(node, src):
    n = node.child_by_field_name('name')
    if n is None: return None
    return src[n.start_byte:n.end_byte].decode('utf8', 'replace')

def parse(src: bytes, lang: str):
    return _parsers[lang].parse(src)

def unit_info(node, lang, src, parentq):
    """Return (kind, name) if node is a unit root, else None."""
    t = node.type
    if lang == 'py':
        if t == 'function_definition': return ('func', _name_of(node, src))
        if t == 'class_definition': return ('class', _name_of(node, src))
        if t == 'expression_statement' and parentq is not None and node.parent is not None \
                and node.parent.type in ('module', 'block') and node.named_child_count == 1 \
                and (node.parent.type == 'module' or (node.parent.parent is not None and node.parent.parent.type == 'class_definition')):
            a = node.named_children[0]
            if a.type == 'assignment':
                l = a.child_by_field_name('left')
                if l is not None and l.type == 'identifier':
                    return ('const', src[l.start_byte:l.end_byte].decode())
        return None
    if lang in ('ts', 'tsx'):
        if t in ('function_declaration', 'generator_function_declaration'): return ('func', _name_of(node, src))
        if t == 'method_definition': return ('func', _name_of(node, src))
        if t in ('class_declaration', 'abstract_class_declaration'): return ('class', _name_of(node, src))
        if t in ('variable_declarator', 'public_field_definition'):
            nm = node.child_by_field_name('name')
            v = node.child_by_field_name('value')
            if nm is None or nm.type != 'identifier' and nm.type != 'property_identifier': return None
            name = src[nm.start_byte:nm.end_byte].decode('utf8', 'replace')
            if v is not None and v.type in ('arrow_function', 'function_expression', 'function'):
                return ('func', name)
            # only top-level / class-level consts
            p = node.parent
            if t == 'public_field_definition' or (p is not None and p.parent is not None and p.parent.type in ('program', 'export_statement')):
                return ('const', name)
        return None
    if lang == 'go':
        if t == 'function_declaration': return ('func', _name_of(node, src))
        if t == 'method_declaration':
            r = node.child_by_field_name('receiver')
            rt = ''
            if r is not None:
                for ch in r.named_children:
                    ty = ch.child_by_field_name('type')
                    if ty is not None:
                        rt = src[ty.start_byte:ty.end_byte].decode().lstrip('*')
                        # strip generics
                        rt = rt.split('[')[0]
            return ('func', (rt + '.' if rt else '') + (_name_of(node, src) or ''), )
        if t in ('const_spec', 'var_spec'):
            nm = node.child_by_field_name('name')
            if nm is not None and node.parent is not None and node.parent.parent is not None and node.parent.parent.type == 'source_file':
                return ('const', src[nm.start_byte:nm.end_byte].decode())
        return None
    return None

def extract(src: bytes, lang: str, path: str):
    tree = parse(src, lang)
    units = []
    def tokens_of(node, root_units_children, out, stmtid_ctx):
        pass
    def walk_unit(node, kind, name, qual, parent):
        u = Unit(); u.path = path; u.qual = qual; u.name = name.split('.')[-1] if name else ''
        u.kind = kind; u.parent = parent
        u.sl = node.start_point[0] + 1; u.el = node.end_point[0] + 1
        u.toks = []; u.norm = []; u.lines = []; u.stmts = []; u.bases = []
        u.text = None
        units.append(u)
        if kind == 'class' and lang == 'py':
            sup = node.child_by_field_name('superclasses')
            if sup is not None:
                u.bases = [src[c.start_byte:c.end_byte].decode('utf8', 'replace') for c in sup.named_children if c.type in ('identifier', 'attribute')]
        if kind == 'class' and lang in ('ts', 'tsx'):
            for c in node.children:
                if c.type == 'class_heritage':
                    for cc in c.named_children:
                        for ccc in cc.named_children:
                            if ccc.type in ('identifier', 'member_expression', 'type_identifier'):
                                u.bases.append(src[ccc.start_byte:ccc.end_byte].decode('utf8', 'replace'))
        collect(node, u, qual, stmt=[None], top=True)
        return u
    def collect(node, u, qual, stmt, top=False):
        # iterate children, emitting tokens to u unless child is a nested unit
        stack = [(node, stmt[0])]
        # explicit recursion for clarity
        def rec(n, cur_stmt):
            for ch in n.children:
                if ch.type in ('comment', 'line_comment', 'block_comment'): continue
                info = unit_info(ch, lang, src, qual)
                if info is not None and info[1]:
                    if u.kind == 'class' or info[0] != 'const' or True:
                        nq = qual + '.' + info[1] if qual else info[1]
                        walk_unit(ch, info[0], info[1], nq, qual)
                        continue
                # python docstring-like expression statement
                if lang == 'py' and ch.type == 'expression_statement' and ch.named_child_count == 1 \
                        and ch.named_children[0].type in ('string', 'concatenated_string'):
                    continue
                nstmt = cur_stmt
                if n.type in BLOCKS or ch.parent is node and False:
                    nstmt = id(ch)
                if ch.type in LIT_ATOM:
                    emit(ch, 'LIT', nstmt)
                elif ch.child_count == 0:
                    ty = ch.type
                    if ty in IDENT: emit(ch, 'ID', nstmt)
                    else: emit(ch, None, nstmt)
                else:
                    rec(ch, nstmt)
        def emit(ch, cls, nstmt):
            txt = src[ch.start_byte:ch.end_byte].decode('utf8', 'replace')
            if not txt.strip(): return
            u.toks.append(txt)
            u.norm.append(cls if cls else txt)
            u.lines.append(ch.start_point[0] + 1)
            u.stmts.append(nstmt)
        rec(node, None)
    root = tree.root_node
    def top(n, qual):
        for ch in n.children:
            info = unit_info(ch, lang, src, qual if qual else '')
            if info is not None and info[1]:
                nq = qual + '.' + info[1] if qual else info[1]
                walk_unit(ch, info[0], info[1], nq, qual)
            elif ch.type in ('comment',):
                continue
            else:
                # descend through export_statement, decorated_definition, lexical_declaration, if blocks etc.
                if ch.child_count: top(ch, qual)
    top(root, '')
    return units, root.has_error
