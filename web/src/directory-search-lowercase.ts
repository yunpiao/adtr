import { directorySearchLowerRanges } from "./directory-search-case-map";

/** Match the backend's Go Unicode simple lowercase, independent of browser ICU. */
export function directorySearchLowercase(value: string): string {
  let result = "";
  for (const character of value) {
    const point = character.codePointAt(0)!;
    let low = 0;
    let high: number = directorySearchLowerRanges.length;
    let mapped = point;
    while (low < high) {
      const middle = Math.floor((low + high) / 2);
      const [first, last, delta] = directorySearchLowerRanges[middle];
      if (point < first) high = middle;
      else if (point > last) low = middle + 1;
      else {
        // Go's UpperLower sentinel denotes alternating uppercase/lowercase
        // pairs starting at first. Lowercase selects the odd member.
        mapped =
          delta === 0x110000 ? first + ((point - first) | 1) : point + delta;
        break;
      }
    }
    result += String.fromCodePoint(mapped);
  }
  return result;
}

/** Fields stay independent: never trim, concatenate, normalize or parse a pattern. */
export function directoryLiteralSearchMatches(
  fields: readonly (string | null)[],
  search: string,
): boolean {
  if (search === "") return true;
  const lowered = directorySearchLowercase(search);
  return fields.some(
    (field) =>
      field !== null && directorySearchLowercase(field).includes(lowered),
  );
}
