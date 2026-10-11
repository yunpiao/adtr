export interface UserAssetsV2Filters {
  sAMAccountName?: string;
  startTm?: string;
  endTm?: string;
  creatTm?: "asc" | "desc";
}

export const validUserAssetsV2SAM = (value: unknown): value is string =>
  typeof value === "string" &&
  value.length <= 256 &&
  !/[\ud800-\udfff]/u.test(value) &&
  new TextEncoder().encode(value).length <= 1024;

const leapYear = (year: number) =>
  year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
const daysBeforeYear = (year: number) => {
  const previous = year - 1;
  return (
    previous * 365 +
    Math.floor(previous / 4) -
    Math.floor(previous / 100) +
    Math.floor(previous / 400)
  );
};
const nanosPerSecond = 1_000_000_000n;
const lastInstantExclusive =
  BigInt(daysBeforeYear(10000)) * 86400n * nanosPerSecond;

// Nanoseconds from 0001-01-01 UTC, never Date milliseconds. This preserves
// half-open boundaries smaller than one millisecond and the actual year 0001.
export function userAssetsV2Time(value: unknown): bigint | undefined {
  if (typeof value !== "string" || value.length > 35) return undefined;
  const match =
    /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))$/u.exec(
      value,
    );
  if (!match || match[0] !== value) return undefined;
  const [, y, mo, d, h, mi, s, fraction, zone, sign, oh, om] = match;
  const year = Number(y),
    month = Number(mo),
    day = Number(d);
  const hour = Number(h),
    minute = Number(mi),
    second = Number(s);
  const offsetHour = zone === "Z" ? 0 : Number(oh);
  const offsetMinute = zone === "Z" ? 0 : Number(om);
  const months = [
    31,
    leapYear(year) ? 29 : 28,
    31,
    30,
    31,
    30,
    31,
    31,
    30,
    31,
    30,
    31,
  ];
  if (
    year < 1 ||
    month < 1 ||
    month > 12 ||
    day < 1 ||
    day > months[month - 1] ||
    hour > 23 ||
    minute > 59 ||
    second > 59 ||
    offsetHour > 23 ||
    offsetMinute > 59
  )
    return undefined;
  const days =
    daysBeforeYear(year) +
    months.slice(0, month - 1).reduce((sum, count) => sum + count, 0) +
    day -
    1;
  const offset =
    (offsetHour * 60 + offsetMinute) * 60 * (sign === "-" ? -1 : 1);
  const seconds =
    BigInt(days) * 86400n + BigInt(hour * 3600 + minute * 60 + second - offset);
  const instant =
    seconds * nanosPerSecond + BigInt((fraction ?? "").padEnd(9, "0"));
  return instant >= 0n && instant < lastInstantExclusive ? instant : undefined;
}

export function validUserAssetsV2Filters(q: UserAssetsV2Filters): boolean {
  if (
    (q.sAMAccountName !== undefined &&
      !validUserAssetsV2SAM(q.sAMAccountName)) ||
    (q.creatTm !== undefined && q.creatTm !== "asc" && q.creatTm !== "desc")
  )
    return false;
  if (q.startTm === undefined && q.endTm === undefined) return true;
  const start = userAssetsV2Time(q.startTm),
    end = userAssetsV2Time(q.endTm);
  return start !== undefined && end !== undefined && start < end;
}
