// Package harness is the repository scenario suite. It verifies user workflows
// against a freshly built application process through public HTTP and stdio
// interfaces, with isolated workspaces and real persistence. Live IOA scenarios
// drive independent model operators and processes.
//
// The suite imports no application implementation package: every scenario runs
// the same executable a user starts from the command line. Package tests that
// need an in-process host use internal/testutil/hosttest and internal/testutil/apptest instead.
package harness
