import { useMemo } from 'react'
import { GraphPanel, type GraphPanelProps } from '@cyber/ioa'
import { harnessGraph } from '../lib/ioa-harness'

export function HarnessGraph(props: Omit<GraphPanelProps, 'extension'>) {
  const extension = useMemo(() => harnessGraph(props.thread.messages), [props.thread.messages])
  return <GraphPanel {...props} extension={extension} />
}
