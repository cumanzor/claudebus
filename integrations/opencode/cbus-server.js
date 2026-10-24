// cbus server plugin: gives each agent shell command the exact OpenCode session
// it runs in, so `cbus connect` and `cbus send` resolve that identity and never a
// session ID inherited from whatever launched OpenCode.
const SESSION_ID = /^ses_[A-Za-z0-9]{20,64}$/;
const INHERITED = ["CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "GROK_SESSION_ID"];

export function sessionEnv(input) {
  const env = {};
  for (const name of INHERITED) env[name] = "";
  const sid = input?.sessionID;
  env.CBUS_SESSION_ID = typeof sid === "string" && SESSION_ID.test(sid) ? sid : "";
  return env;
}

export default {
  id: "cbus.server",
  server: async () => ({
    "shell.env": async (input, output) => {
      Object.assign(output.env, sessionEnv(input));
    },
  }),
};
