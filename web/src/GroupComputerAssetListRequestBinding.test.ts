import { afterEach, describe, expect, it, vi } from "vitest";
import {
  computerAssetsV3API,
  type ComputerAssetsV3Query,
} from "./computer-assets-v3-api";
import {
  groupAssetsV3API,
  type GroupAssetsV3Query,
} from "./group-assets-v3-api";

type ListQuery = ComputerAssetsV3Query & GroupAssetsV3Query;

const filters: ListQuery = {
  domainId: "fixture-domain",
  expectedRevision: "9007199254740993",
  expectedCredentialRevision: "9007199254740995",
  search: "original",
  pageIdx: 1,
  pageSize: 10,
  creatTm: "asc",
};

const cases = [
  {
    kind: "group",
    api: groupAssetsV3API,
    countField: "observedGroupCount",
    dateFilters: {},
  },
  {
    kind: "computer",
    api: computerAssetsV3API,
    countField: "observedComputerCount",
    dateFilters: {
      startTm: "2026-10-10T00:00:00Z",
      endTm: "2026-10-11T00:00:00Z",
    },
  },
] as const;

afterEach(() => vi.unstubAllGlobals());

describe.each(cases)("$kind assets list request binding", (testCase) => {
  it.each(["original", "edited"] as const)(
    "attaches validation to the sent query rather than a later draft (%s response)",
    async (responseFor) => {
      const original: ListQuery = { ...filters, ...testCase.dateFilters };
      const draft = { ...original };
      const edited: ListQuery = {
        ...draft,
        expectedRevision: "9007199254740997",
        expectedCredentialRevision: "9007199254740999",
        search: "edited",
        pageSize: 20,
        creatTm: "desc",
      };
      let respond!: (value: Response) => void;
      const pending = new Promise<Response>((resolve) => {
        respond = resolve;
      });
      const fetcher = vi.fn<typeof fetch>().mockReturnValue(pending);
      vi.stubGlobal("fetch", fetcher);

      const response = testCase.api.list(
        draft,
        18,
        new AbortController().signal,
      );
      expect(fetcher).toHaveBeenCalledTimes(1);
      const [url, options] = fetcher.mock.calls[0];
      const sent = new URL(String(url), "https://test.invalid");
      expect(sent.pathname).toBe(`/api/${testCase.kind}-assets/v3`);
      expect(Object.fromEntries(sent.searchParams)).toEqual(
        Object.fromEntries(
          Object.entries(original).map(([key, value]) => [key, String(value)]),
        ),
      );
      expect(options?.method).toBe("GET");

      const resultQuery = responseFor === "original" ? original : edited;
      Object.assign(draft, edited);
      const payload = {
        dictionaryVersion: 3,
        selection: {
          domainId: resultQuery.domainId,
          revision: resultQuery.expectedRevision,
          credentialRevision: resultQuery.expectedCredentialRevision,
        },
        available: false,
        [testCase.countField]: null,
        list: [],
        page: {
          pageIdx: resultQuery.pageIdx,
          pageSize: resultQuery.pageSize,
          total: 0,
          totalPage: 0,
        },
      };
      const assertion =
        responseFor === "original"
          ? expect(response).resolves.toEqual(payload)
          : expect(response).rejects.toMatchObject({
              code: "invalid_response",
            });
      respond(
        new Response(JSON.stringify(payload), {
          headers: {
            "Content-Type": "application/json",
            "X-ADTR-User-ID": "18",
          },
        }),
      );
      await assertion;
    },
  );
});
