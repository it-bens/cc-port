package progress

// Noop returns a Reporter that swallows every event. It is the default a
// command carries when progress reporting is off, so terminal methods on a
// no-op phase handle swallow rather than panic: a latent misuse must never
// crash a real command merely because progress happens to be disabled.
func Noop() Reporter {
	return noopReporter{}
}

type noopReporter struct{}

func (noopReporter) Phase(string, int64, Unit) PhaseHandle { return noopHandle{} }
func (noopReporter) Detail(Level, string, ...any)          {}
func (noopReporter) Warn(error)                            {}
func (noopReporter) Done()                                 {}
func (noopReporter) Fail(error)                            {}
func (noopReporter) Cancelled(string)                      {}

type noopHandle struct{}

func (noopHandle) Phase(string, int64, Unit) PhaseHandle    { return noopHandle{} }
func (noopHandle) SubPhase(string, int64, Unit) PhaseHandle { return noopHandle{} }
func (noopHandle) Detail(Level, string, ...any)             {}
func (noopHandle) Warn(error)                               {}
func (noopHandle) Advance(int64)                            {}
func (noopHandle) End(string)                               {}
func (noopHandle) Done()                                    {}
func (noopHandle) Fail(error)                               {}
func (noopHandle) Cancelled(string)                         {}
