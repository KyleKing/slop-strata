# explain

You are given JSON from `slop-strata change --json --context`: the scored
result plus the hunks, source lines, and commit messages behind it. Answer for
a reviewer who will look at the code next, not at a dashboard.

`result.next` is the tool's own ordered list of up to three places to look, each
with a reason built from the numbers, and `result.context.snippets` holds the
source lines of each function in it. `result.top_functions` ranks the ten functions
the change grew most. An empty `next` means nothing crossed the bar for a look.

1. Does the stated intent match what the numbers show? If the reading flags a
   disagreement (a refactor that adds mass, a cleanup that raises erosion), say
   what in the diff explains it.
2. Which one function should the reviewer look at first, and why? Start from
   the first `grown_function` item in `next`, or from the top of `top_functions`
   when `next` has none. Name it and the line, and read its snippet before you
   agree with the ranking.
3. For each finding: real or false positive? Give the line that decides it.
   A finding a person marks goes back to the labels file the rule loop reads.
   If you go on to change the code, repair what the finding names. Renaming
   it, moving it, wrapping it in a helper, or adding a comment leaves the
   problem where it was, and most such edits leave the finding too. A
   finding that is right and should stay takes `slop-strata:ignore <rule-id>
   <reason>` on its line, with the reason a reviewer would accept.
   An `x/new-duplicate` finding's `note` names the code the new lines repeat.
   Read both sites before you agree, and when it is right, call or widen
   that code: a copy under new names is the finding again. A note that
   starts "repeats an expression of" is about one expression inside a
   statement, and the repair is one name both places use.
   An `x/new-case-set` finding's `note` names the places that already
   enumerate the cases the new switch, chain, or table lists. When it is
   right, read from the table or call the function that is there: a second
   list of the same cases is one more place every new case must reach.
   An `x/new-jsx-shape` finding's `note` names the places that already hold
   the markup the new block repeats, and how many values the sites differ
   in. When it is right, make the block one component that takes those
   values as props and use it at both sites.
   An `x/new-sibling` finding's `note` names the classes, types, or modules
   the new one is written like, and the members they share. When it is
   right, extend what is there: a shared base, a generic, or a table of
   what differs.
4. What would a smaller version of this change look like? Concretely: which
   function or file to split off, not general advice.

Never restate numbers without the sentence they form. If the data cannot
answer a question, say which number is missing rather than guessing.
