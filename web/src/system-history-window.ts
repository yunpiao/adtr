// Explicit UTC text avoids datetime-local's browser timezone and calendar normalization.
export function parseHistoryUTC(value: string): number | null {
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$/.test(value)) return null;
  const milliseconds = Date.parse(value);
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return null;
  if (new Date(milliseconds).toISOString() !== value.replace("Z", ".000Z"))
    return null;
  return milliseconds / 1000;
}
export function fixedHistoryWindow(start: string, end: string) {
  const startTime = parseHistoryUTC(start),
    endTime = parseHistoryUTC(end);
  if (
    startTime === null ||
    endTime === null ||
    endTime <= startTime ||
    endTime - startTime > 86400
  )
    return null;
  return { startTime, endTime };
}
