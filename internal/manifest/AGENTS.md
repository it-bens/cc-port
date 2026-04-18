# internal/manifest — agent notes

Wire DTOs and the nine-category enum table for `metadata.xml`. See `README.md` for the full contract.

## Before editing

- The nine export categories live in `AllCategories`; never hard-code a
  parallel list in another package (README §Category manifest).
- `ApplyCategoryEntries` is the only validator for a parsed manifest —
  callers that need a typed `CategorySet` go through it, and both missing
  and unknown names hard-fail via aggregated `errors.Join` (README §Category manifest).
- Wire types (`Metadata`, `Info`, `Category`, `Placeholder`) must stay
  byte-identical on the wire — do not rename fields or change XML tags
  (README §Category manifest).
- Slice order of `AllCategories` is the canonical display and archive
  order; reordering it is a user-visible change (README §Category manifest).

## Navigation

- Enum table: `categories.go:AllCategories`, `categories.go:BuildCategoryEntries`, `categories.go:ApplyCategoryEntries`.
- Wire types + I/O: `manifest.go:Metadata`, `manifest.go:WriteManifest`, `manifest.go:ReadManifest`, `manifest.go:ReadManifestFromZip`.
- Tests: `categories_test.go`, `manifest_test.go`.

Read `README.md` before changing anything under `## Contracts`.
