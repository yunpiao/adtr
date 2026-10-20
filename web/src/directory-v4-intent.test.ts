// Pure projected-storage grammar tests; no mutation primitive is exported.
import { describe, expect, it } from "vitest";
import { directoryV4IntentPrefix, directoryV4OwnerKey, parseDirectoryV4Intent, validDirectoryV4Input } from "./directory-v4-intent";
const owner = { ID:7, username:"synthetic" };
const input = {domainId:"synthetic-domain",expectedRevision:"9007199254740993",expectedCredentialGeneration:"9007199254740995",idempotencyKey:"synthetic-key"};
const record = () => ({...input,storageVersion:1,dictionaryVersion:4,kind:"domain.directory_read.v4",payloadVersion:1,owner,receiptOnly:false});
describe("closed nonsecret intent grammar", () => {
  it("preserves exact bigint strings and isolated owner namespace", () => {
    expect(parseDirectoryV4Intent(record(),owner)).toEqual(record());
    expect(directoryV4IntentPrefix + directoryV4OwnerKey(owner)).toBe("adtr.directory-sync-intent.v4:7:synthetic");
    expect(validDirectoryV4Input(input)).toBe(true);
  });
  it.each(["password","actorPassword","totpCode","csrfToken","result","tenantId"])("rejects extra field %s", key => {
    expect(() => parseDirectoryV4Intent({...record(),[key]:"secret"},owner)).toThrow();
  });
  it.each([{owner:{ID:8,username:"other"}},{dictionaryVersion:3},{storageVersion:2},{kind:"domain.directory_read.v3"},{payloadVersion:2},
    {expectedRevision:9007199254740993},{receiptOnly:undefined},{cancelState:"unknown"},{taskUUID:"original-task",cancelState:"rejected"}])("rejects wrong shape %j", patch => {
    expect(() => parseDirectoryV4Intent({...record(),...patch},owner)).toThrow();
  });
  it.each(["unknown","accepted","terminal"])("accepts task-bound %s marker", cancelState => {
    expect(parseDirectoryV4Intent({...record(),taskUUID:"original-task",cancelState},owner).cancelState).toBe(cancelState);
  });
  it("rejects extra sync fields instead of transmitting them", () => {
    expect(validDirectoryV4Input({...input,actorPassword:"secret"})).toBe(false);
  });
});
