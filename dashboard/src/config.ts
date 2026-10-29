export interface AppConfig {
  apiBaseUrl: string;
  demoMode: boolean;
  timeoutMs: number;
  environmentLabel: string;
}

type EnvLike = Record<string, string | boolean | undefined>;

/** Parse the public (browser-visible) environment into a typed config. */
export function loadConfig(env: EnvLike = import.meta.env as EnvLike): AppConfig {
  const timeout = Number(env.VITE_API_TIMEOUT_MS);
  return {
    apiBaseUrl: String(env.VITE_API_BASE_URL ?? '/api'),
    demoMode: String(env.VITE_DEMO_MODE ?? 'false').toLowerCase() === 'true',
    timeoutMs: Number.isFinite(timeout) && timeout > 0 ? timeout : 10_000,
    environmentLabel: String(env.VITE_ENVIRONMENT_LABEL ?? 'Development'),
  };
}
