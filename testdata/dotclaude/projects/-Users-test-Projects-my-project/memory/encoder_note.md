---
name: encoder_note
description: This directory's name is the lossy encoding of "/Users/test/Projects/my project"
type: project
---

`EncodePath("/Users/test/Projects/my project")` and `EncodePath("/Users/test/Projects/my-project")`
both produce `-Users-test-Projects-my-project`. The original cannot be recovered from the
directory name alone — recovery must use `cwd`/`projectPath` from inside the data files.
