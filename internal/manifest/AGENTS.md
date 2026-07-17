# internal/manifest — agent notes

## Before editing

- Never hard-code a parallel category list in another package; categories are declared per-tool via `Tool.Categories()`. (README §Category manifest)
- Always route manifest validation through `ApplyToolCategories`. (README §Category manifest)
- Never rename wire-type fields or change XML tags. (README §Category manifest)
- Keep placeholders nested under their owning `<tool>` block; do not hoist them to a shared top-level list. (README §Category manifest)

## Navigation

- Category validation: `categories.go:BuildToolCategoryEntries`, `categories.go:ApplyToolCategories`.
- Wire types + I/O: `manifest.go:Metadata`, `manifest.go:Tool`, `manifest.go:WriteManifest`, `manifest.go:ReadManifest`, `manifest.go:ReadManifestFromZip`.
- Tests: `categories_test.go`, `manifest_test.go`.
