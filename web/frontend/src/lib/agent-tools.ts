export function isCompletionTool(toolName: string): boolean {
  return toolName.trim().toLowerCase() === 'finish'
}
