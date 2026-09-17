// Package harness is the repository test harness. It owns two roles:
//
// Scenarios verify user workflows against a freshly built application process using
// public HTTP and stdio interfaces, isolated workspaces and real persistence.
// Live IOA scenarios use independent model operators and processes.
//
// The exported constructors build owned extension hosts for package tests.
// Production code must construct its explicit profile instead.
package harness
