# internal/manifest — agent notes

## Before editing

- Never hard-code a parallel category list in another package. (README §Category manifest)
- Always route manifest validation through `ApplyCategoryEntries`. (README §Category manifest)
- Never rename wire-type fields or change XML tags. (README §Category manifest)
- Never reorder `AllCategories`. (README §Category manifest)

## Navigation

- Enum table: `categories.go:AllCategories`, `categories.go:BuildCategoryEntries`, `categories.go:ApplyCategoryEntries`.
- Wire types + I/O: `manifest.go:Metadata`, `manifest.go:WriteManifest`, `manifest.go:ReadManifest`, `manifest.go:ReadManifestFromZip`.
- Tests: `categories_test.go`, `manifest_test.go`.
