import type { components } from './generated';
export type Schema = components['schemas'];
export type Post = Schema['Post'];
export type Receipt = Schema['Receipt'];
export type Community = Schema['Community'];
export type Me = Schema['Me'];
export class APIError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  options: { method?: string; body?: unknown; version?: number; key?: string } = {},
): Promise<T> {
  const headers: Record<string, string> = {
    'content-type': 'application/json',
    'x-jansetu-csrf': '1',
  };
  if (options.version !== undefined) headers['if-match'] = `"${options.version}"`;
  if (options.key) headers['idempotency-key'] = options.key;
  const response = await fetch('/api/' + path, {
    method: options.method || 'GET',
    headers,
    body: options.body === undefined ? undefined : JSON.stringify(options.body),
    credentials: 'same-origin',
    cache: 'no-store',
  });
  if (response.status === 204) return undefined as T;
  const data = await response.json();
  if (!response.ok)
    throw new APIError(
      response.status,
      data.code || 'REQUEST_FAILED',
      data.title || 'Please try again',
    );
  return data as T;
}
export function readable(state: string) {
  return state
    .toLowerCase()
    .replaceAll('_', ' ')
    .replace(/^./, (c) => c.toUpperCase());
}
export function ago(date: string) {
  const mins = Math.max(1, Math.floor((Date.now() - new Date(date).getTime()) / 60000));
  if (mins < 60) return `${mins}m`;
  if (mins < 1440) return `${Math.floor(mins / 60)}h`;
  return `${Math.floor(mins / 1440)}d`;
}
export function dateLabel(date: string) {
  return new Date(date).toLocaleDateString('en-IN', {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
  });
}
