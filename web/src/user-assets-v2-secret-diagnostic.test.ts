import { describe, expect, it } from "vitest";
import {
  credentialLeakDiagnostic,
  type CredentialCategory,
} from "../e2e/user-assets-v2-secret-diagnostic";

const categories: CredentialCategory[] = [
  "password",
  "account_name",
  "mfa_seed",
  "totp",
  "csrf",
];

// Mirrors only the unchanged assertion predicate; the diagnostic cannot grant
// permission to accept either a whole-value or a substring credential match.
function assertWriteOnly(
  value: unknown,
  secret: string,
  category: CredentialCategory,
) {
  const serialized = JSON.stringify(value);
  const matched = serialized.includes(secret);
  if (matched)
    throw new Error(
      JSON.stringify(credentialLeakDiagnostic(serialized, secret, category)),
    );
}

describe("user asset credential failure diagnostics", () => {
  for (const category of categories) {
    for (const embedded of [false, true]) {
      it(`${category} ${embedded ? "embedded" : "whole"} fails`, () => {
        const secret = "654321";
        const value = {
          observationId: embedded ? `prefix-${secret}-suffix` : secret,
        };
        expect(() => assertWriteOnly(value, secret, category)).toThrow();
        expect(
          credentialLeakDiagnostic(JSON.stringify(value), secret, category),
        ).toEqual({
          credentialCategory: category,
          locationCategory: "observation_id",
          exactValueMatch: !embedded,
          embeddedMatch: embedded,
        });
      });
    }
  }

  it("reports both match forms without their values", () => {
    const secret = "synthetic-private-probe";
    const result = credentialLeakDiagnostic(
      JSON.stringify({
        observationId: secret,
        list: [{ mail: `before${secret}after` }],
      }),
      secret,
      "password",
    );
    expect(result).toEqual({
      credentialCategory: "password",
      locationCategory: "unknown",
      exactValueMatch: true,
      embeddedMatch: true,
    });
    expect(JSON.stringify(result).includes(secret)).toBe(false);
  });

  it("never exports unknown field names or nested paths", () => {
    const key = "private-dynamic-field";
    const secret = "synthetic-private-probe";
    const value = { [key]: { observationId: secret } };
    const result = credentialLeakDiagnostic(
      JSON.stringify(value),
      secret,
      "csrf",
    );
    expect(result).toEqual({
      credentialCategory: "csrf",
      locationCategory: "unknown",
      exactValueMatch: true,
      embeddedMatch: false,
    });
    expect(Object.keys(result).sort()).toEqual([
      "credentialCategory",
      "embeddedMatch",
      "exactValueMatch",
      "locationCategory",
    ]);
    for (const forbidden of [key, secret, "observationId"])
      expect(JSON.stringify(result).includes(forbidden)).toBe(false);
  });

  it("keeps matches in unknown keys fatal and unclassified", () => {
    const secret = "synthetic-private-probe";
    const value = { [`field-${secret}`]: "unrelated" };
    expect(() => assertWriteOnly(value, secret, "mfa_seed")).toThrow();
    expect(
      credentialLeakDiagnostic(JSON.stringify(value), secret, "mfa_seed"),
    ).toEqual({
      credentialCategory: "mfa_seed",
      locationCategory: "unknown",
      exactValueMatch: false,
      embeddedMatch: true,
    });
  });

  it("recognizes only literal known schema locations", () => {
    const secret = "654321";
    const cases = [
      [{ selection: { domainId: secret } }, "selection"],
      [{ list: [{ objectGUID: secret }] }, "object_guid"],
      [{ object: { objectSid: `S-${secret}` } }, "object_sid"],
      [{ list: [{ description: [secret] }] }, "asset_text"],
      [{ object: { whenCreated: secret } }, "asset_timestamp"],
      [{ page: { total: 654321 } }, "pagination"],
      [{ source: { pages: 654321 } }, "source_counter"],
      [{ source: { elapsed_milliseconds: 654321 } }, "source_counter"],
      [{ source: { started_at: secret } }, "source_timestamp"],
      [{ source: { completed_at: secret } }, "source_timestamp"],
      [{ source: { server_name: secret } }, "source_identity"],
      [{ list: { objectGUID: secret } }, "unknown"],
      [{ unknown: { list: [{ objectGUID: secret }] } }, "unknown"],
    ] as const;
    for (const [value, location] of cases) {
      expect(() => assertWriteOnly(value, secret, "totp")).toThrow();
      expect(
        credentialLeakDiagnostic(JSON.stringify(value), secret, "totp")
          .locationCategory,
      ).toBe(location);
    }
  });

  it("does not invoke input getters again", () => {
    let reads = 0;
    const value = {
      get observationId() {
        reads++;
        return "synthetic-private-probe";
      },
    };
    const serialized = JSON.stringify(value);
    credentialLeakDiagnostic(serialized, "synthetic-private-probe", "password");
    expect(reads).toBe(1);
  });

  it("keeps cross-token and invalid JSON diagnostics closed", () => {
    for (const serialized of ['{"a":"x","b":"y"}', "private-invalid-json"]) {
      const secret = serialized.includes(",") ? '","b":"' : "invalid";
      expect(serialized.includes(secret)).toBe(true);
      const result = credentialLeakDiagnostic(serialized, secret, "totp");
      expect(result).toEqual({
        credentialCategory: "totp",
        locationCategory: "unknown",
        exactValueMatch: false,
        embeddedMatch: true,
      });
      expect(JSON.stringify(result).includes(secret)).toBe(false);
    }
  });

  it("handles same-category matches, scalars and escaping", () => {
    for (const [value, secret] of [
      [null, "null"],
      [true, "true"],
      [654321, "654321"],
    ] as const) {
      const result = credentialLeakDiagnostic(
        JSON.stringify({ page: { total: value } }),
        secret,
        "totp",
      );
      expect(result).toEqual({
        credentialCategory: "totp",
        locationCategory: "pagination",
        exactValueMatch: true,
        embeddedMatch: false,
      });
    }
    const result = credentialLeakDiagnostic(
      JSON.stringify({
        source: { started_at: "654321", completed_at: "x654321" },
      }),
      "654321",
      "totp",
    );
    expect(result).toEqual({
      credentialCategory: "totp",
      locationCategory: "source_timestamp",
      exactValueMatch: true,
      embeddedMatch: true,
    });
    const escaped = JSON.stringify({ object: { mail: "line\nnext" } });
    expect(escaped.includes("line\nnext")).toBe(false);
    expect(() =>
      assertWriteOnly(
        { object: { mail: "line\nnext" } },
        "line\nnext",
        "password",
      ),
    ).not.toThrow();
    const encodedSecret = "line\\nnext";
    expect(
      credentialLeakDiagnostic(escaped, encodedSecret, "password"),
    ).toEqual({
      credentialCategory: "password",
      locationCategory: "asset_text",
      exactValueMatch: false,
      embeddedMatch: true,
    });
  });

  it("rejects an unrecognized category without reflecting it", () => {
    const malicious = "private-category-payload";
    let failure: unknown;
    try {
      credentialLeakDiagnostic(
        '{"observationId":"654321"}',
        "654321",
        malicious as CredentialCategory,
      );
    } catch (error) {
      failure = error;
    }
    expect(failure).toBeInstanceOf(Error);
    expect((failure as Error).message).toBe(
      "Invalid credential diagnostic category",
    );
    expect((failure as Error).message.includes(malicious)).toBe(false);
  });

  it("does not reject an unrelated serialized response", () => {
    expect(() =>
      assertWriteOnly(
        { observationId: "public-observation" },
        "654321",
        "totp",
      ),
    ).not.toThrow();
  });
});
