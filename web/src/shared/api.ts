import { t, type Message } from '@/shared/settings.svelte';

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
  ) {
    super(message);
  }
}
export let unauthorized: () => void = () => {};
export function onUnauthorized(handler: () => void) {
  unauthorized = handler;
}
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const response = await fetch(`/api/${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...options.headers },
  });
  const value = await response.json();
  if (response.status === 401) unauthorized();
  if (!response.ok) {
    const messages: Record<string, Message> = {
      'invalid token': 'loginError',
      'node name already exists': 'nameTaken',
      'invalid node name': 'invalidName',
      'node admission state does not allow this operation': 'admissionError',
      'node offline or busy': 'busy',
    };
    throw new ApiError(
      response.status,
      messages[value.error]
        ? t(messages[value.error])
        : value.error || `HTTP ${response.status}`,
      value.code,
    );
  }
  return value;
}
export function output(value?: string) {
  return value
    ? new TextDecoder().decode(
        Uint8Array.from(atob(value), (c) => c.charCodeAt(0)),
      )
    : '';
}
