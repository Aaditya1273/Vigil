/**
 * Backend location, resolved once.
 *
 * Every route handler used to hardcode a deployed hostname, which meant a
 * locally-running control plane was unreachable from the dashboard no matter
 * what you set. Server-side only: this is read inside route handlers, never
 * shipped to the browser, so it needs no NEXT_PUBLIC_ prefix.
 */
export const BACKEND =
  process.env.VIGIL_BACKEND_URL ||
  process.env.ARGUS_BACKEND_URL || // pre-rename spelling, still honored
  "http://localhost:8080";

/** Absolute URL for a Vigil API path, e.g. api("/vigil/stats"). */
export function api(path: string): string {
  return `${BACKEND}/api/v1${path.startsWith("/") ? path : `/${path}`}`;
}

/**
 * Proxy a GET to the backend, returning `fallback` when it is unreachable.
 *
 * The dashboard polls continuously, so a backend that is down should render an
 * empty panel rather than a wall of errors. Each caller picks the empty shape
 * its component expects.
 */
export async function proxyGet<T>(path: string, fallback: T): Promise<T> {
  try {
    const res = await fetch(api(path), { cache: "no-store" });
    if (!res.ok) return fallback;
    return (await res.json()) as T;
  } catch {
    return fallback;
  }
}
