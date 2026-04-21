package claude

// VerifyProjectIdentityForTest exposes verifyProjectIdentity so fuzz tests in
// package claude_test can exercise the guard without routing through
// LocateProject. Production code continues to reach the guard only via
// LocateProject.
var VerifyProjectIdentityForTest = verifyProjectIdentity
