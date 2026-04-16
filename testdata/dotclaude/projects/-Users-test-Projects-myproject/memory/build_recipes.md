---
name: build_recipes
description: How to build, test, and lint the project at /Users/test/Projects/myproject
type: reference
---

```sh
cd /Users/test/Projects/myproject
go build ./...
go test ./...
```

Integration tests live under the build tag `integration`:

```sh
go test -tags=integration ./...
```
