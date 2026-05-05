---
name: writing-go-code
description: Use when writing or editing Go code in cc-port — any change to a `**/*.go` file that adds, modifies, or removes implementation logic, signatures, or comments.
---

# Writing Go Code

## Workflow

```dot
digraph go_code_workflow {
    entry [shape=doublecircle, label="About to write\nor edit Go code"];
    call_decision [shape=diamond, label="External library\ncall in scope?"];
    certain [shape=diamond, label="100% certain of\nsignature & semantics?"];
    consult [shape=box, label="go doc\n<pkg>.<Symbol>"];
    write [shape=box, label="Write the call\nor edit the line"];
    comment_decision [shape=diamond, label="Comment proposed,\nkept, or edited?"];
    classify [shape=box, label="Classify\nthe comment"];
    apply [shape=box, label="Apply the\nclassified action"];
    more [shape=diamond, label="More to write\nor edit?"];
    done [shape=doublecircle, label="Done"];

    entry -> call_decision;
    call_decision -> certain [label="yes"];
    call_decision -> comment_decision [label="no"];
    certain -> write [label="yes"];
    certain -> consult [label="no"];
    consult -> write;
    write -> comment_decision;
    comment_decision -> classify [label="yes"];
    comment_decision -> more [label="no"];
    classify -> apply;
    apply -> more;
    more -> call_decision [label="yes"];
    more -> done [label="no"];
}
```

### Confirm the API call

Before writing or editing a call to any Go library symbol — stdlib or third-party — ask: am I 100% certain of this symbol's signature, error behavior, and edge-case semantics, or am I pattern-matching from training? Builtins (`len`, `append`, `cap`, `make`, `delete`, `copy`) proceed. Anything else: `go doc <pkg>.<Symbol>` first.

Skip when: the call mechanically repeats an idiom already established in the same file, or the call already compiles and a test exercising it passes.

Load `references/go-doc.md` for the query-shape table (single symbol vs `-short` vs package overview vs full dump with sizes), the escalation criteria to `-src`, and the don't-consult exceptions.

### Classify each comment

Default: write no comment. Only write or keep one when it carries point-of-use *why* — a hidden constraint, subtle invariant, bug workaround, or deliberate tradeoff that the reader cannot infer from the code. When deleting code, also delete any why-comment above it; an orphaned why is dead weight.

For every comment proposed, kept, or edited, classify it and apply the listed action. Load `references/comments.md` for the full classification table (load-bearing why / exported godoc / explains-what / tutorial / over-specified / redundant-with-README), a worked load-bearing-why example, the banned-pattern examples (no `// see README §X` backlinks, no line-numbered cross-refs), and the godoc compression rule for exported symbols.
