package cli

// RequiredProduct exposes the unexported requiredProduct for external test
// packages (cli_test). internal/cli/pbs and internal/cli/pve both import
// internal/cli, so an in-package (package cli) test file cannot import them
// without creating an import cycle; this export lets root_test.go (package
// cli_test) exercise requiredProduct against commands built by those
// packages' ChildFactories.
var RequiredProduct = requiredProduct

// RedactArgs exposes the unexported redactArgs for cli_test's audit-record
// redaction tests.
var RedactArgs = redactArgs

// InvocationArgs exposes the unexported invocationArgs so cli_test can cover
// the passthrough-args redaction branch (DisableFlagParsing and the
// AnnotationPassthroughArgs annotation) without wiring a full rsync-style
// delegating command.
var InvocationArgs = invocationArgs
