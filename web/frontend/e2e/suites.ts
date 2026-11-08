// Source suites run against Vite, not the Go server's embedded production UI.
export const uiTestFiles = [
  'scan-ux.spec.ts',
  'recap-presentation.spec.ts', 'terminal-rendering.spec.ts', 'plugin-runtime.spec.ts',
  'ioa-graph.spec.ts', 'ioa-web.spec.ts',
]
export const jevTestFiles = [
  'jev-motion.spec.ts', 'workflow.spec.ts', 'jev-ui.spec.ts', 'jev-decision-ui.spec.ts',
  'jev-presentation.spec.ts', 'jev-replay.spec.ts', 'jev-live-replay.spec.ts', 'jev-robustness.spec.ts',
  'jev-mechanism.spec.ts',
  'jev-live-acceptance.spec.ts',
]
