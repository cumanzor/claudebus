export type QuietReplies = string[]

declare module 'claude-code' {
  interface PluginState {
    'cbus-compact': { quietReplies: Shaped<QuietReplies> }
  }
}
