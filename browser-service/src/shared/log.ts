export type Log = (event: string, values?: Record<string, unknown>) => void;

export function createLog(enabled: boolean): Log {
  return (event, values = {}) => {
    if (!enabled) return;
    process.stderr.write(`${JSON.stringify({ component: "browser-service", event, ...values })}\n`);
  };
}
