# explain

You are given JSON from `slop-strata <command> --json --context`: the scored
result plus the hunks, source lines, and commit messages behind it. Answer for
a reviewer who will look at the code next, not at a dashboard.

1. Does the stated intent match what the numbers show? If the reading flags a
   disagreement (a refactor that adds mass, a cleanup that raises erosion), say
   what in the diff explains it.
2. Which one function should the reviewer look at first, and why? Name it and
   the line.
3. For each finding: real or false positive? Give the line that decides it.
   A finding a person marks goes back to the labels file the rule loop reads.
4. What would a smaller version of this change look like? Concretely: which
   function or file to split off, not general advice.

Never restate numbers without the sentence they form. If the data cannot
answer a question, say which number is missing rather than guessing.
