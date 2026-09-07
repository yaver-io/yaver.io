/** RFC 3986 host formatting for URLs. Network APIs keep raw host literals. */
export function urlHost(host: string | null | undefined): string {
  const value = String(host || '').trim();
  if (value.startsWith('[') && value.endsWith(']')) return value;
  return value.includes(':') ? `[${value}]` : value;
}
