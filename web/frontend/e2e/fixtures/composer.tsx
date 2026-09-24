import React from 'react'
import { createRoot } from 'react-dom/client'
import ChatInput from '../../cyber-ui/packages/viewer/src/components/chat/ChatInput'
import { TooltipProvider } from '../../cyber-ui/packages/ui/src'
const state = window as unknown as { submissions: { content: string; files: number }[]; settle: (accepted: boolean) => void }
state.submissions = []
createRoot(document.getElementById('root')!).render(<TooltipProvider><ChatInput onSend={(content, attachments) => {
  state.submissions.push({ content, files: attachments?.length || 0 })
  return new Promise<boolean>((resolve) => { state.settle = resolve })
}} enableAttachments labels={{ sendMessage: 'Send message' }} /></TooltipProvider>)
