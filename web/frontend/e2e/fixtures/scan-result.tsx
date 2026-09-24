import React from 'react'
import { createRoot } from 'react-dom/client'
import { TooltipProvider } from '../../cyber-ui/packages/ui/src'
import '../../src/i18n'
import ScannerToolCall from '../../src/components/chat/ScannerToolCall'

createRoot(document.getElementById('root')!).render(<TooltipProvider><ScannerToolCall id="outer-call" toolName="scan" toolArgs="-i 127.0.0.1" result="scan failed; collected evidence retained" error /></TooltipProvider>)
