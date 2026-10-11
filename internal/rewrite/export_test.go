package rewrite

// SetRenameFunc overrides the rename implementation so tests in package
// rewrite_test can inject promote-time failures such as EXDEV.
func (p *SafeRenamePromoter) SetRenameFunc(fn func(oldpath, newpath string) error) {
	p.renameFunc = fn
}
