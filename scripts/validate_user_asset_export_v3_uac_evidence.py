"""Independent mixed33 evidence gate. No fixture or production predicate imports.

The frozen design schema is embedded so this gate has no mutable schema input or
new dependency. Only the reviewed literal oracle is an external expected value.
All errors are fixed codes; raw JSON/BER/request values never enter diagnostics.
"""
import base64
from datetime import datetime
import hashlib
import json
import math
import os
import stat
from pathlib import Path
import re
import struct
import uuid

from validate_user_asset_export_v3_evidence import workbook_rows, validate_png

PROFILE = "mixed-33-v1"
ORACLE_SHA256 = "833245587fd68cca1f32f3b140703b8b18a13332a94f04e7f8c83db30a1d210b"
RAW_NAME = "user-asset-export-v3-uac-raw-wire.json"
MODES = ("set", "clear", "not_returned")
CODES = frozenset("configuration provenance candidate_discovery candidate_schema source_binding task_binding raw_transcript terminal_seal post_parse post_binding post_count artifact_binding cleanup publication".split())
# Exact reviewed handoff definitions, mechanically embedded rather than inferred
# from a specimen, runtime output, fixture generator or producer implementation.
SCHEMA = json.loads(r'''{"ID":{"type":"string","pattern":"^[A-Za-z0-9_.-]{1,128}$","not":{"const":"platform"}},"Revision":{"type":"string","pattern":"^[1-9][0-9]{0,18}$","maxLength":19,"description":"Semantic check <=9223372036854775807, canonical unsigned decimal."},"ActorDecimal":{"type":"string","pattern":"^[1-9][0-9]{0,18}$","maxLength":19,"description":"Semantic check <=9223372036854775807, canonical unsigned decimal."},"Hash":{"type":"string","pattern":"^[0-9a-f]{64}$"},"Nonce":{"type":"string","pattern":"^[0-9a-f]{32}$"},"Base64":{"type":"string","pattern":"^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$","description":"Decode then re-encode equality required, decoded byte cap at semantic gate."},"Time":{"type":"string","pattern":"^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\\.[0-9]{1,9})?Z$","maxLength":30},"Selection":{"type":"object","additionalProperties":false,"properties":{"domainId":{"$ref":"#/$defs/ID"},"revision":{"$ref":"#/$defs/Revision"},"credentialRevision":{"$ref":"#/$defs/Revision"}},"required":["domainId","revision","credentialRevision"]},"SourcePins":{"type":"object","additionalProperties":false,"properties":{"accountId":{"$ref":"#/$defs/ID"},"accountCredentialRevision":{"$ref":"#/$defs/Revision"},"policyRevision":{"$ref":"#/$defs/Hash"},"grantRoleId":{"type":"string","pattern":"^(?:platform_admin|[A-Za-z0-9_-]{24})$"},"grantRevision":{"$ref":"#/$defs/Revision"}},"required":["accountId","accountCredentialRevision","policyRevision","grantRoleId","grantRevision"]},"ProducerPayload":{"type":"object","additionalProperties":false,"properties":{"credentialSource":{"const":"operation_account"},"connectionRevision":{"$ref":"#/$defs/Revision"},"connectionCredentialGeneration":{"$ref":"#/$defs/Revision"},"accountId":{"$ref":"#/$defs/ID"},"accountCredentialRevision":{"$ref":"#/$defs/Revision"},"grantRoleId":{"type":"string","pattern":"^(?:platform_admin|[A-Za-z0-9_-]{24})$"},"grantRevision":{"$ref":"#/$defs/Revision"},"policyRevision":{"$ref":"#/$defs/Hash"},"dictionaryVersion":{"const":3}},"required":["credentialSource","connectionRevision","connectionCredentialGeneration","accountId","accountCredentialRevision","grantRoleId","grantRevision","policyRevision","dictionaryVersion"]},"ExportRequest":{"type":"object","additionalProperties":false,"properties":{"domainId":{"$ref":"#/$defs/ID"},"expectedRevision":{"$ref":"#/$defs/Revision"},"expectedCredentialRevision":{"$ref":"#/$defs/Revision"},"observationId":{"$ref":"#/$defs/ID"},"search":{"const":"user-"},"sAMAccountName":{"const":""},"startTm":{"const":""},"endTm":{"const":""},"creatTm":{"const":"desc"},"screenColumns":{"const":["mail","guid","sAMAccountName","dn","sid","whenCreated"]},"uacDisableBit":{"enum":["set","clear","not_returned"]}},"required":["domainId","expectedRevision","expectedCredentialRevision","observationId","search","sAMAccountName","startTm","endTm","creatTm","screenColumns","uacDisableBit"]},"ProducerTaskBinding":{"type":"object","additionalProperties":false,"properties":{"payloadVersion":{"const":1},"parentTaskUUID":{"const":""},"payloadHash":{"$ref":"#/$defs/Hash"},"payload":{"$ref":"#/$defs/ProducerPayload"}},"required":["payloadVersion","parentTaskUUID","payloadHash","payload"]},"ExportTaskBinding":{"type":"object","additionalProperties":false,"properties":{"payloadVersion":{"const":1},"parentTaskUUID":{"const":""},"payloadHash":{"$ref":"#/$defs/Hash"},"payload":{"$ref":"#/$defs/ExportRequest"}},"required":["payloadVersion","parentTaskUUID","payloadHash","payload"]},"SourceEvidence":{"type":"object","additionalProperties":false,"properties":{"taskUUID":{"$ref":"#/$defs/ID"},"actorId":{"$ref":"#/$defs/ActorDecimal"},"selection":{"$ref":"#/$defs/Selection"},"sourcePins":{"$ref":"#/$defs/SourcePins"},"bodyBase64":{"$ref":"#/$defs/Base64"},"sha256":{"$ref":"#/$defs/Hash"},"dictionaryVersion":{"const":3},"count":{"const":51},"taskState":{"const":"succeeded"},"taskName":{"const":"domain.directory_read.v3"},"payloadVersion":{"const":1},"maxAttempts":{"const":1},"attempt":{"const":1},"purpose":{"const":"domain.directory_read.v3"},"useState":{"const":"quiesced"},"reason":{"const":"executor_returned"},"dependencies":{"const":0},"taskBinding":{"$ref":"#/$defs/ProducerTaskBinding"}},"required":["taskUUID","actorId","selection","sourcePins","bodyBase64","sha256","dictionaryVersion","count","taskState","taskName","payloadVersion","maxAttempts","attempt","purpose","useState","reason","dependencies","taskBinding"]},"DirectorySource":{"type":"object","additionalProperties":false,"properties":{"server_name":{"const":"dc.synthetic.invalid"},"dc_host_name":{"const":"dc.synthetic.invalid"},"domain":{"const":"synthetic.invalid"},"naming_context":{"const":"DC=synthetic,DC=invalid"},"started_at":{"$ref":"#/$defs/Time"},"completed_at":{"$ref":"#/$defs/Time"},"elapsed_milliseconds":{"type":"integer","minimum":0,"maximum":120000},"pages":{"const":4}},"required":["server_name","dc_host_name","domain","naming_context","started_at","completed_at","elapsed_milliseconds","pages"]},"BaseObject":{"type":"object","additionalProperties":false,"properties":{"objectGUID":{"type":"string","pattern":"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},"distinguishedName":{"type":"string","maxLength":2048},"kind":{"enum":["user","group","computer"]},"objectClass":{"type":"array","items":{"type":"string","maxLength":128},"minItems":1,"maxItems":8},"samAccountName":{"anyOf":[{"type":"string","maxLength":128},{"type":"null"}]},"userAccountControl":{"anyOf":[{"type":"integer","minimum":0,"maximum":4294967295},{"type":"null"}]}},"required":["objectGUID","distinguishedName","kind","objectClass","samAccountName","userAccountControl"]},"Supplemental":{"type":"object","additionalProperties":false,"properties":{"objectSidBytes":{"anyOf":[{"$ref":"#/$defs/Base64"},{"type":"null"}]},"mailBytes":{"anyOf":[{"$ref":"#/$defs/Base64"},{"type":"null"}]},"descriptionBytes":{"anyOf":[{"type":"array","items":{"$ref":"#/$defs/Base64"},"minItems":1,"maxItems":8},{"type":"null"}]},"whenCreatedBytes":{"anyOf":[{"$ref":"#/$defs/Base64"},{"type":"null"}]}},"required":["objectSidBytes","mailBytes","descriptionBytes","whenCreatedBytes"]},"Identity":{"type":"object","additionalProperties":false,"properties":{"primaryGroupIDBytes":{"anyOf":[{"$ref":"#/$defs/Base64"},{"type":"null"}]},"servicePrincipalNameBytes":{"anyOf":[{"type":"array","items":{"$ref":"#/$defs/Base64"},"minItems":1,"maxItems":128},{"type":"null"}]}},"required":["primaryGroupIDBytes","servicePrincipalNameBytes"]},"StoredObject":{"type":"object","additionalProperties":false,"properties":{"base":{"$ref":"#/$defs/BaseObject"},"supplemental":{"$ref":"#/$defs/Supplemental"},"identity":{"$ref":"#/$defs/Identity"}},"required":["base","supplemental","identity"]},"DecodedObservation":{"type":"object","additionalProperties":false,"properties":{"dictionaryVersion":{"const":3},"objects":{"type":"array","items":{"$ref":"#/$defs/StoredObject"},"minItems":51,"maxItems":51},"source":{"$ref":"#/$defs/DirectorySource"}},"required":["dictionaryVersion","objects","source"]},"PublicUser":{"type":"object","additionalProperties":false,"properties":{"objectGUID":{"type":"string","pattern":"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},"distinguishedName":{"type":"string","maxLength":2048},"kind":{"const":"user"},"objectClass":{"type":"array","items":{"type":"string","maxLength":128},"minItems":1,"maxItems":8},"samAccountName":{"anyOf":[{"type":"string","maxLength":128},{"type":"null"}]},"userAccountControl":{"anyOf":[{"type":"integer","minimum":0,"maximum":4294967295},{"type":"null"}]},"objectSid":{"anyOf":[{"type":"string","maxLength":256},{"type":"null"}]},"mail":{"anyOf":[{"type":"string","maxLength":4096},{"type":"null"}]},"description":{"anyOf":[{"type":"array","items":{"type":"string","maxLength":4096},"minItems":1,"maxItems":8},{"type":"null"}]},"whenCreated":{"anyOf":[{"$ref":"#/$defs/Time"},{"type":"null"}]},"primaryGroupID":{"anyOf":[{"type":"integer","minimum":0,"maximum":4294967295},{"type":"null"}]},"servicePrincipalName":{"anyOf":[{"type":"array","items":{"type":"string","maxLength":4096},"minItems":1,"maxItems":128},{"type":"null"}]}},"required":["objectGUID","distinguishedName","kind","objectClass","samAccountName","userAccountControl","objectSid","mail","description","whenCreated","primaryGroupID","servicePrincipalName"]},"Page":{"type":"object","additionalProperties":false,"properties":{"pageIdx":{"type":"integer","minimum":1,"maximum":4},"pageSize":{"const":10},"total":{"enum":[11,33]},"totalPage":{"enum":[2,4]}},"required":["pageIdx","pageSize","total","totalPage"]},"UserListResponse":{"type":"object","additionalProperties":false,"properties":{"dictionaryVersion":{"const":3},"selection":{"$ref":"#/$defs/Selection"},"available":{"const":true},"observationId":{"$ref":"#/$defs/ID"},"source":{"$ref":"#/$defs/DirectorySource"},"list":{"type":"array","items":{"$ref":"#/$defs/PublicUser"},"minItems":1,"maxItems":10},"page":{"$ref":"#/$defs/Page"}},"required":["dictionaryVersion","selection","available","observationId","source","list","page"]},"QueryEntries":{"type":"array","items":{"type":"array","prefixItems":[{"type":"string","maxLength":128},{"type":"string","maxLength":2048}],"items":false,"minItems":2,"maxItems":2},"minItems":8,"maxItems":9},"ListPageEvidence":{"type":"object","additionalProperties":false,"properties":{"mode":{"enum":["omitted","set","clear","not_returned"]},"pageIdx":{"type":"integer","minimum":1,"maximum":4},"queryEntries":{"$ref":"#/$defs/QueryEntries"},"response":{"$ref":"#/$defs/UserListResponse"}},"required":["mode","pageIdx","queryEntries","response"]},"Budget":{"type":"object","additionalProperties":false,"properties":{"owner":{"type":"integer","minimum":7,"maximum":10},"producer":{"const":9},"observations":{"const":1},"directoryUses":{"const":1},"exportTasks":{"type":"integer","minimum":0,"maximum":3}},"required":["owner","producer","observations","directoryUses","exportTasks"]},"SensitiveBudget":{"type":"object","additionalProperties":false,"properties":{"before":{"$ref":"#/$defs/Budget"},"afterEach":{"type":"array","items":{"$ref":"#/$defs/Budget"},"minItems":3,"maxItems":3},"after":{"$ref":"#/$defs/Budget"}},"required":["before","afterEach","after"]},"PublicTask":{"type":"object","additionalProperties":false,"properties":{"taskUUID":{"$ref":"#/$defs/ID"},"terminalAt":{"anyOf":[{"$ref":"#/$defs/Time"},{"type":"null"}]},"archived":{"const":false},"visibilityVersion":{"type":"integer","minimum":0,"maximum":9007199254740991},"taskName":{"const":"user_assets.v3.export"},"domainId":{"$ref":"#/$defs/ID"},"payloadVersion":{"const":1},"state":{"enum":["queued","running","succeeded"]},"sourceState":{"enum":["PENDING","STARTED","SUCCESS"]},"error":{"const":""},"createdAt":{"$ref":"#/$defs/Time"},"updatedAt":{"$ref":"#/$defs/Time"},"attempt":{"type":"integer","minimum":0,"maximum":1},"maxAttempts":{"const":3},"progress":{"type":"integer","minimum":0,"maximum":100},"resultVersion":{"type":"integer","minimum":0,"maximum":9007199254740991},"result":{"type":"object","additionalProperties":false,"properties":{},"required":[]},"cursor":{"type":"object","additionalProperties":false,"properties":{},"required":[]},"parentTaskUUID":{"const":""},"nextAttemptAt":{"$ref":"#/$defs/Time"}},"required":["taskUUID","terminalAt","archived","visibilityVersion","taskName","domainId","payloadVersion","state","sourceState","error","createdAt","updatedAt","attempt","maxAttempts","progress","resultVersion","result","cursor","parentTaskUUID"]},"Artifact":{"type":"object","additionalProperties":false,"properties":{"rowCount":{"const":11},"byteCount":{"type":"integer","minimum":1,"maximum":33554432},"sha256":{"$ref":"#/$defs/Hash"},"filename":{"type":"string","pattern":"^user-assets-v3-[A-Za-z0-9_.-]{1,128}\\.xlsx$"}},"required":["rowCount","byteCount","sha256","filename"]},"Receipt":{"type":"object","additionalProperties":false,"properties":{"taskID":{"$ref":"#/$defs/ID"},"task":{"$ref":"#/$defs/PublicTask"},"request":{"$ref":"#/$defs/ExportRequest"}},"required":["taskID","task","request"]},"Create":{"type":"object","additionalProperties":false,"properties":{"taskID":{"$ref":"#/$defs/ID"},"task":{"$ref":"#/$defs/PublicTask"},"request":{"$ref":"#/$defs/ExportRequest"},"replayed":{"const":false}},"required":["taskID","task","request","replayed"]},"Detail":{"type":"object","additionalProperties":false,"properties":{"taskID":{"$ref":"#/$defs/ID"},"task":{"$ref":"#/$defs/PublicTask"},"request":{"$ref":"#/$defs/ExportRequest"},"downloadReady":{"const":true},"downloadStatus":{"const":"ready"},"artifact":{"$ref":"#/$defs/Artifact"}},"required":["taskID","task","request","downloadReady","downloadStatus","artifact"]},"Headers":{"type":"object","additionalProperties":false,"properties":{"x-adtr-user-id":{"$ref":"#/$defs/ActorDecimal"},"cache-control":{"const":"no-store"},"x-content-type-options":{"const":"nosniff"},"content-type":{"const":"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},"content-length":{"type":"string","pattern":"^[1-9][0-9]*$"},"x-adtr-artifact-sha256":{"$ref":"#/$defs/Hash"},"x-adtr-row-count":{"const":"11"},"content-disposition":{"type":"string","maxLength":200}},"required":["x-adtr-user-id","cache-control","x-content-type-options","content-type","content-length","x-adtr-artifact-sha256","x-adtr-row-count","content-disposition"]},"Download":{"type":"object","additionalProperties":false,"properties":{"filename":{"type":"string","pattern":"^user-assets-v3-[A-Za-z0-9_.-]{1,128}\\.xlsx$"},"headers":{"$ref":"#/$defs/Headers"}},"required":["filename","headers"]},"Chunk":{"type":"object","additionalProperties":false,"properties":{"ordinal":{"type":"integer","minimum":0,"maximum":31},"bodyBase64":{"$ref":"#/$defs/Base64"},"sha256":{"$ref":"#/$defs/Hash"}},"required":["ordinal","bodyBase64","sha256"]},"TaskEvent":{"type":"object","additionalProperties":false,"properties":{"action":{"enum":["submitted","leased","started","progress","finished"]},"state":{"enum":["queued","running","succeeded"]},"resultVersion":{"type":"integer","minimum":0,"maximum":9007199254740991}},"required":["action","state","resultVersion"]},"StoredExport":{"type":"object","additionalProperties":false,"properties":{"taskID":{"$ref":"#/$defs/ID"},"actorId":{"$ref":"#/$defs/ActorDecimal"},"state":{"const":"succeeded"},"kind":{"const":"user_assets.v3.export"},"progress":{"const":100},"attempt":{"const":1},"maxAttempts":{"const":3},"sourceSHA256":{"$ref":"#/$defs/Hash"},"rowCount":{"const":11},"byteCount":{"type":"integer","minimum":1,"maximum":33554432},"sha256":{"$ref":"#/$defs/Hash"},"chunkCount":{"type":"integer","minimum":1,"maximum":32},"chunks":{"type":"array","items":{"$ref":"#/$defs/Chunk"},"minItems":1,"maxItems":32},"events":{"type":"array","items":{"$ref":"#/$defs/TaskEvent"},"minItems":5,"maxItems":256},"taskBinding":{"$ref":"#/$defs/ExportTaskBinding"},"idempotencyKeySHA256":{"$ref":"#/$defs/Hash"}},"required":["taskID","actorId","state","kind","progress","attempt","maxAttempts","sourceSHA256","rowCount","byteCount","sha256","chunkCount","chunks","events","taskBinding","idempotencyKeySHA256"]},"Screenshot":{"type":"object","additionalProperties":false,"properties":{"filename":{"enum":["set-ready.png","clear-ready.png","not_returned-ready.png"]},"sha256":{"$ref":"#/$defs/Hash"}},"required":["filename","sha256"]},"OriginalTaskReadback":{"type":"object","additionalProperties":false,"properties":{"detail":{"$ref":"#/$defs/Detail"},"download":{"$ref":"#/$defs/Download"},"downloadSHA256":{"$ref":"#/$defs/Hash"},"downloadByteCount":{"type":"integer","minimum":1,"maximum":33554432}},"required":["detail","download","downloadSHA256","downloadByteCount"]},"Case":{"type":"object","additionalProperties":false,"properties":{"name":{"enum":["set","clear","not_returned"]},"request":{"$ref":"#/$defs/ExportRequest"},"create":{"$ref":"#/$defs/Create"},"receipt":{"$ref":"#/$defs/Receipt"},"detail":{"$ref":"#/$defs/Detail"},"download":{"$ref":"#/$defs/Download"},"database":{"$ref":"#/$defs/StoredExport"},"pageSize":{"const":10},"firstPageCount":{"const":10},"sourcePageIdx":{"const":2},"screenshot":{"$ref":"#/$defs/Screenshot"},"listQueryEntries":{"$ref":"#/$defs/QueryEntries"},"originalTaskReadback":{"$ref":"#/$defs/OriginalTaskReadback"}},"required":["name","request","create","receipt","detail","download","database","pageSize","firstPageCount","sourcePageIdx","screenshot","listQueryEntries","originalTaskReadback"]},"PostEvent":{"type":"object","additionalProperties":false,"properties":{"ordinal":{"type":"integer","minimum":1,"maximum":3},"case":{"enum":["set","clear","not_returned"]},"method":{"const":"POST"},"pathname":{"const":"/api/user-assets/v3/exports"},"bodyFieldNames":{"const":["actorPassword","creatTm","domainId","endTm","expectedCredentialRevision","expectedRevision","idempotencyKey","observationId","sAMAccountName","screenColumns","search","startTm","totpCode","uacDisableBit"]},"safeBusinessRequest":{"$ref":"#/$defs/ExportRequest"},"idempotencyKeySHA256":{"$ref":"#/$defs/Hash"},"sanitizedRequestSHA256":{"$ref":"#/$defs/Hash"},"status":{"const":200},"transport":{"const":"response"},"taskID":{"$ref":"#/$defs/ID"}},"required":["ordinal","case","method","pathname","bodyFieldNames","safeBusinessRequest","idempotencyKeySHA256","sanitizedRequestSHA256","status","transport","taskID"]},"PostLedger":{"type":"object","additionalProperties":false,"properties":{"events":{"type":"array","items":{"$ref":"#/$defs/PostEvent"},"minItems":3,"maxItems":3},"createCount":{"const":3},"cancelCount":{"const":0},"unexpectedExportWriteCount":{"const":0},"listenerClosed":{"const":true},"finalSensitiveSnapshot":{"$ref":"#/$defs/Budget"}},"required":["events","createCount","cancelCount","unexpectedExportWriteCount","listenerClosed","finalSensitiveSnapshot"]},"CleanupReceipt":{"type":"object","additionalProperties":false,"properties":{"format":{"const":1},"profile":{"const":"mixed-33-v1"},"nonce":{"$ref":"#/$defs/Nonce"},"workerExitCode":{"const":0},"apiExitCode":{"const":0},"ldapExitCode":{"const":0},"ldapContainerRemoved":{"const":true},"ldapNetworkRemoved":{"const":true},"ldapPrivateDirectoriesRemoved":{"const":true},"databaseContainerRemoved":{"const":true},"logsClosed":{"const":true},"safeDiagnosticsPassed":{"const":true},"allOwnedCleanupSucceeded":{"const":true}},"required":["format","profile","nonce","workerExitCode","apiExitCode","ldapExitCode","ldapContainerRemoved","ldapNetworkRemoved","ldapPrivateDirectoriesRemoved","databaseContainerRemoved","logsClosed","safeDiagnosticsPassed","allOwnedCleanupSucceeded"]},"Provenance":{"type":"object","additionalProperties":false,"properties":{"checkoutCommit":{"type":"string","pattern":"^[0-9a-f]{40}$"},"checkoutTree":{"type":"string","pattern":"^[0-9a-f]{40}$"},"runID":{"type":"string","pattern":"^[1-9][0-9]*$"},"runAttempt":{"type":"string","pattern":"^[1-9][0-9]*$"},"sourceSHA256":{"type":"object","minProperties":1,"additionalProperties":{"$ref":"#/$defs/Hash"},"propertyNames":{"type":"string","pattern":"^(?:cmd/|internal/|scripts/|web/src/|web/e2e/|\\.github/workflows/|web/playwright\\.config\\.ts$|web/package(?:-lock)?\\.json$|go\\.(?:mod|sum)$|Makefile$)[^\\u0000]*$"},"description":"Exact key set equals trusted git ls-files over existing SOURCE_ROOTS; cannot omit/add source paths."}},"required":["checkoutCommit","checkoutTree","runID","runAttempt","sourceSHA256"]},"RawWireReference":{"type":"object","additionalProperties":false,"properties":{"filename":{"const":"user-asset-export-v3-uac-raw-wire.json"},"sha256":{"$ref":"#/$defs/Hash"},"candidateTranscriptSHA256":{"$ref":"#/$defs/Hash"},"terminalSealSHA256":{"$ref":"#/$defs/Hash"}},"required":["filename","sha256","candidateTranscriptSHA256","terminalSealSHA256"]},"CandidateEvidence":{"type":"object","additionalProperties":false,"properties":{"format":{"const":1},"profile":{"const":"mixed-33-v1"},"provenance":{"$ref":"#/$defs/Provenance"},"source":{"$ref":"#/$defs/SourceEvidence"},"actorId":{"type":"integer","minimum":1,"maximum":9007199254740991},"sensitiveBudget":{"$ref":"#/$defs/SensitiveBudget"},"cases":{"type":"array","items":{"$ref":"#/$defs/Case"},"minItems":3,"maxItems":3},"listPages":{"type":"array","items":{"$ref":"#/$defs/ListPageEvidence"},"minItems":10,"maxItems":10},"postLedger":{"$ref":"#/$defs/PostLedger"},"oracleSHA256":{"const":"833245587fd68cca1f32f3b140703b8b18a13332a94f04e7f8c83db30a1d210b"},"nonce":{"$ref":"#/$defs/Nonce"},"candidateTranscriptSHA256":{"$ref":"#/$defs/Hash"}},"required":["format","profile","provenance","source","actorId","sensitiveBudget","cases","listPages","postLedger","oracleSHA256","nonce","candidateTranscriptSHA256"]},"FinalEvidence":{"type":"object","additionalProperties":false,"properties":{"format":{"const":1},"profile":{"const":"mixed-33-v1"},"provenance":{"$ref":"#/$defs/Provenance"},"source":{"$ref":"#/$defs/SourceEvidence"},"actorId":{"type":"integer","minimum":1,"maximum":9007199254740991},"sensitiveBudget":{"$ref":"#/$defs/SensitiveBudget"},"cases":{"type":"array","items":{"$ref":"#/$defs/Case"},"minItems":3,"maxItems":3},"listPages":{"type":"array","items":{"$ref":"#/$defs/ListPageEvidence"},"minItems":10,"maxItems":10},"postLedger":{"$ref":"#/$defs/PostLedger"},"oracleSHA256":{"const":"833245587fd68cca1f32f3b140703b8b18a13332a94f04e7f8c83db30a1d210b"},"rawWire":{"$ref":"#/$defs/RawWireReference"},"cleanupReceipt":{"$ref":"#/$defs/CleanupReceipt"}},"required":["format","profile","provenance","source","actorId","sensitiveBudget","cases","listPages","postLedger","oracleSHA256","rawWire","cleanupReceipt"]}}''' )

class UACEvidenceError(ValueError):
    def __init__(self, code):
        super().__init__('uac_evidence_invalid:' + (code if code in CODES else 'candidate_schema'))


def require(condition, code='candidate_schema'):
    if not condition:
        raise UACEvidenceError(code)


def exact(value, keys, code='candidate_schema'):
    require(type(value) is dict and set(value) == set(keys), code)


def sha(raw):
    return hashlib.sha256(raw).hexdigest()


def scalar_tree(value, depth=0):
    require(depth <= 48)
    if type(value) is str:
        require(not any(0xd800 <= ord(c) <= 0xdfff for c in value))
    elif type(value) is dict:
        for k, v in value.items():
            require(type(k) is str)
            scalar_tree(k, depth + 1)
            scalar_tree(v, depth + 1)
    elif type(value) is list:
        for v in value:
            scalar_tree(v, depth + 1)
    elif type(value) is float:
        require(math.isfinite(value))
    else:
        require(value is None or type(value) in (bool, int))


def strict_json(raw, cap=8 << 20, code='candidate_schema'):
    try:
        require(type(raw) is bytes and 0 < len(raw) <= cap and not raw.startswith(b'\xef\xbb\xbf'), code)
        def pairs(entries):
            result = {}
            for key, value in entries:
                require(key not in result, code)
                result[key] = value
            return result
        value = json.loads(raw.decode('utf-8', 'strict'), object_pairs_hook=pairs,
                           parse_constant=lambda _: (_ for _ in ()).throw(UACEvidenceError(code)))
        scalar_tree(value)
        return value
    except Exception:
        raise UACEvidenceError(code) from None


def equal(a, b):
    # JSON equality must not equate true/1 or 1.0/1, including nested constants.
    return type(a) is type(b) and (set(a) == set(b) and all(equal(a[k], b[k]) for k in a)
        if type(a) is dict else len(a) == len(b) and all(equal(x, y) for x, y in zip(a, b))
        if type(a) is list else a == b)


def schema(value, definition, code='candidate_schema'):
    s = SCHEMA[definition] if type(definition) is str else definition
    if type(s) is bool:
        require(s, code)
        return
    if '$ref' in s:
        return schema(value, s['$ref'].rsplit('/', 1)[1], code)
    if 'anyOf' in s:
        for option in s['anyOf']:
            try:
                schema(value, option, code)
                return
            except UACEvidenceError:
                pass
        raise UACEvidenceError(code)
    if 'const' in s:
        require(equal(value, s['const']), code)
    if 'enum' in s:
        require(any(equal(value, candidate) for candidate in s['enum']), code)
    if 'not' in s:
        try:
            schema(value, s['not'], code)
        except UACEvidenceError:
            pass
        else:
            raise UACEvidenceError(code)
    kind = s.get('type')
    if kind:
        require(type(value) is {'object': dict, 'array': list, 'string': str, 'integer': int,
                               'boolean': bool, 'null': type(None)}[kind], code)
    if type(value) is dict:
        props = s.get('properties', {})
        require(set(s.get('required', ())) <= set(value), code)
        require(s.get('minProperties', 0) <= len(value), code)
        additional = s.get('additionalProperties', True)
        require(additional is not False or set(value) <= set(props), code)
        for key, item in value.items():
            if 'propertyNames' in s:
                schema(key, s['propertyNames'], code)
            if key in props:
                schema(item, props[key], code)
            elif type(additional) is dict:
                schema(item, additional, code)
    if type(value) is list:
        require(s.get('minItems', 0) <= len(value) <= s.get('maxItems', 100000), code)
        if s.get('uniqueItems'):
            require(len({json.dumps(x, sort_keys=True) for x in value}) == len(value), code)
        prefix = s.get('prefixItems', [])
        for i, item in enumerate(value):
            if i < len(prefix):
                schema(item, prefix[i], code)
            elif 'items' in s:
                schema(item, s['items'], code)
    if type(value) is str:
        require(s.get('minLength', 0) <= len(value) <= s.get('maxLength', 16 << 20), code)
        if 'pattern' in s:
            require(re.fullmatch(s['pattern'], value) is not None, code)
    if type(value) is int:
        require(s.get('minimum', -2**63) <= value <= s.get('maximum', 2**63-1), code)
    if definition in ('Revision', 'ActorDecimal') if type(definition) is str else False:
        require(int(value) <= 9223372036854775807, code)
    if definition == 'Time':
        instant(value, code)


def instant(value, code='source_binding'):
    try:
        require(type(value) is str and re.fullmatch(r'[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?Z', value) is not None, code)
        second = datetime.fromisoformat(value[:19] + '+00:00')
        fraction = value[20:-1] if value[19:20] == '.' else ''
        return second, int(fraction.ljust(9, '0') or '0')
    except Exception:
        raise UACEvidenceError(code) from None


def b64(value, cap, code='artifact_binding'):
    try:
        require(type(value) is str and len(value) <= 4 * ((cap + 2) // 3), code)
        raw = base64.b64decode(value, validate=True)
        require(base64.b64encode(raw).decode('ascii') == value and len(raw) <= cap, code)
        return raw
    except Exception:
        raise UACEvidenceError(code) from None


def task_hash(payload):
    return sha(b'1::' + json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8'))


def request_for(source, mode):
    return {'domainId': source['selection']['domainId'], 'expectedRevision': source['selection']['revision'],
            'expectedCredentialRevision': source['selection']['credentialRevision'], 'observationId': source['taskUUID'],
            'search': 'user-', 'sAMAccountName': '', 'startTm': '', 'endTm': '', 'creatTm': 'desc',
            'screenColumns': ['mail', 'guid', 'sAMAccountName', 'dn', 'sid', 'whenCreated'], 'uacDisableBit': mode}


def safe_request_hash(request):
    # Deliberately client insertion order, NOT task canonical sorted-key domain.
    keys = ('domainId', 'expectedRevision', 'expectedCredentialRevision', 'observationId', 'search',
            'sAMAccountName', 'startTm', 'endTm', 'creatTm', 'screenColumns', 'uacDisableBit')
    return sha(json.dumps({k: request[k] for k in keys}, ensure_ascii=False, separators=(',', ':')).encode('utf-8'))


class BER:
    """Strict definite, shortest-form BER cursor; offsets remain original-frame offsets."""
    def __init__(self, raw, start=0, end=None):
        self.raw, self.pos, self.end = raw, start, len(raw) if end is None else end

    def take(self, tag):
        require(self.pos + 2 <= self.end and self.raw[self.pos] == tag, 'raw_transcript')
        self.pos += 1
        size = self.raw[self.pos]
        self.pos += 1
        if size & 128:
            count = size & 127
            require(1 <= count <= 4 and self.pos + count <= self.end and self.raw[self.pos] != 0, 'raw_transcript')
            size = int.from_bytes(self.raw[self.pos:self.pos + count], 'big')
            self.pos += count
            require(size >= 128 and size.bit_length() > 8 * (count - 1), 'raw_transcript')
        start = self.pos
        self.pos += size
        require(self.pos <= self.end, 'raw_transcript')
        return BER(self.raw, start, self.pos)

    def done(self):
        require(self.pos == self.end, 'raw_transcript')

    def bytes(self):
        raw = self.raw[self.pos:self.end]
        self.pos = self.end
        return raw

    def integer(self, tag=2):
        raw = self.take(tag).bytes()
        require(1 <= len(raw) <= 8 and not raw[0] & 128 and
                (len(raw) == 1 or raw[0] != 0 or raw[1] & 128), 'raw_transcript')
        return int.from_bytes(raw, 'big')


def message(raw, op):
    root = BER(raw)
    seq = root.take(0x30)
    root.done()
    mid = seq.integer()
    body = seq.take(op)
    return mid, body, seq


def entry_wire(raw):
    mid, body, tail = message(raw, 0x64)
    dn = body.take(4).bytes().decode('utf-8', 'strict')
    attrs = body.take(0x30)
    body.done()
    tail.done()
    values = []
    seen = set()
    while attrs.pos < attrs.end:
        attr = attrs.take(0x30)
        name = attr.take(4).bytes().decode('ascii', 'strict')
        require(name not in seen, 'raw_transcript')
        seen.add(name)
        contents = attr.take(0x31)
        attr.done()
        items = []
        while contents.pos < contents.end:
            items.append(contents.take(4).bytes())
        require(bool(items), 'raw_transcript')
        values.append((name, items))
    return mid, dn, values


def literal_wire(record):
    values = []
    for attr in record['wire']['attributesInWireOrder']:
        values.append((attr['name'], [bytes.fromhex(v) for v in attr['valuesHex']] if 'valuesHex' in attr
                       else [v.encode('utf-8') for v in attr['valuesUTF8']]))
    return record['wire']['dn'], values


def decoded_public(dn, values):
    """Independent byte decoder, never calls production kind/filter/projection."""
    a = dict(values)
    def one(key):
        if key not in a:
            return None
        require(len(a[key]) == 1, 'source_binding')
        return a[key][0]
    def text(key):
        v = one(key)
        return None if v is None else v.decode('utf-8', 'strict')
    def decimal(key):
        v = text(key)
        if v is None:
            return None
        require(re.fullmatch(r'0|[1-9][0-9]{0,9}', v) is not None and int(v) <= 4294967295, 'source_binding')
        return int(v)
    classes = sorted(v.decode('ascii').lower() for v in a['objectClass'])
    kind = 'computer' if 'computer' in classes else 'group' if 'group' in classes else 'user'
    guid = one('objectGUID')
    require(guid is not None and len(guid) == 16, 'source_binding')
    sid = one('objectSid')
    if sid is not None:
        require(len(sid) >= 8 and sid[0] == 1 and len(sid) == 8 + 4 * sid[1], 'source_binding')
        sid = 'S-1-' + str(int.from_bytes(sid[2:8], 'big')) + ''.join('-' + str(x[0]) for x in struct.iter_unpack('<I', sid[8:]))
    created = text('whenCreated')
    if created is not None:
        require(re.fullmatch(r'[0-9]{14}\.0Z', created) is not None, 'source_binding')
        created = f'{created[:4]}-{created[4:6]}-{created[6:8]}T{created[8:10]}:{created[10:12]}:{created[12:14]}Z'
        instant(created)
    return {'objectGUID': str(uuid.UUID(bytes_le=guid)), 'distinguishedName': dn, 'kind': kind,
            'objectClass': classes, 'samAccountName': text('sAMAccountName'), 'userAccountControl': decimal('userAccountControl'),
            'objectSid': sid, 'mail': text('mail'), 'description': [v.decode('utf-8') for v in a['description']] if 'description' in a else None,
            'whenCreated': created, 'primaryGroupID': decimal('primaryGroupID'),
            'servicePrincipalName': [v.decode('utf-8') for v in a['servicePrincipalName']] if 'servicePrincipalName' in a else None}


def stored_object(dn, values):
    pub = decoded_public(dn, values)
    a = dict(values)
    def raw(key, many=False):
        if key not in a:
            return None
        result = [base64.b64encode(v).decode('ascii') for v in a[key]]
        return result if many else result[0]
    return {'base': {k: pub[k] for k in ('objectGUID', 'distinguishedName', 'kind', 'objectClass', 'samAccountName', 'userAccountControl')},
            'supplemental': {'objectSidBytes': raw('objectSid'), 'mailBytes': raw('mail'), 'descriptionBytes': raw('description', True), 'whenCreatedBytes': raw('whenCreated')},
            'identity': {'primaryGroupIDBytes': raw('primaryGroupID'), 'servicePrincipalNameBytes': raw('servicePrincipalName', True)}}


def done_wire(raw, wrapped, message_id, page_idx):
    mid, result, tail = message(raw, 0x65)
    require(mid == message_id and result.integer(10) == 0 and result.take(4).bytes() == b'' and result.take(4).bytes() == b'', 'raw_transcript')
    result.done()
    controls = tail.take(0xa0)
    tail.done()
    control = controls.take(0x30)
    controls.done()
    require(control.take(4).bytes() == b'1.2.840.113556.1.4.319', 'raw_transcript')
    value = control.take(4)
    control.done()  # Explicit criticality/extra controls are rejected.
    paging = value.take(0x30)
    value.done()
    require(paging.integer() == 51, 'raw_transcript')
    cookie = paging.take(4)
    paging.done()
    length = 0 if page_idx == 4 else 20
    require(type(wrapped['cookieValueOffset']) is int and type(wrapped['cookieValueLength']) is int and
            wrapped['cookieValueOffset'] == cookie.pos and wrapped['cookieValueLength'] == length and
            cookie.end - cookie.pos == length and cookie.bytes() == bytes(length), 'raw_transcript')
    require(wrapped['redactedFrameSHA256'] == sha(raw), 'raw_transcript')
    if page_idx == 4:
        require(wrapped['originalFrameSHA256'] == sha(raw) and wrapped['cookieSHA256'] == sha(b''), 'raw_transcript')


def validate_wire(raw, source, oracle, nonce):
    envelope = strict_json(raw, 256 << 10, 'raw_transcript')
    exact(envelope, 'format profile nonce candidateTranscriptBase64 candidateTranscriptSHA256 terminalSealBase64 terminalSealSHA256'.split(), 'raw_transcript')
    require(type(envelope['format']) is int and envelope['format'] == 1 and envelope['profile'] == PROFILE and envelope['nonce'] == nonce, 'raw_transcript')
    candidate_bytes = b64(envelope['candidateTranscriptBase64'], 160 << 10, 'raw_transcript')
    seal_bytes = b64(envelope['terminalSealBase64'], 8 << 10, 'terminal_seal')
    require(sha(candidate_bytes) == envelope['candidateTranscriptSHA256'] and sha(seal_bytes) == envelope['terminalSealSHA256'], 'raw_transcript')
    candidate = strict_json(candidate_bytes, 160 << 10, 'raw_transcript')
    seal = strict_json(seal_bytes, 8 << 10, 'terminal_seal')
    exact(candidate, 'format profile nonce transport claimOrdinal entryCount pageCount completionSequence pages'.split(), 'raw_transcript')
    expected = {'format': 1, 'profile': PROFILE, 'nonce': nonce, 'claimOrdinal': 1, 'entryCount': 51, 'pageCount': 4}
    require(all(equal(candidate[k], v) for k, v in expected.items()) and candidate['transport'] == 'starttls', 'raw_transcript')
    require(type(candidate['completionSequence']) is int and candidate['completionSequence'] > 0, 'raw_transcript')
    seal_expected = {'format': 1, 'profile': PROFILE, 'nonce': nonce, 'candidateTranscriptSHA256': sha(candidate_bytes),
        'claimCount': 1, 'completedExchangeCount': 1, 'directoryAttemptCount': 4, 'pageCount': 4, 'entryCount': 51,
        'activeHandlers': 0, 'inflightWrites': 0, 'listenersClosed': True, 'participantClosed': True,
        'poisoned': False, 'poisonCode': 'none', 'lateDirectoryAttempts': 0, 'participantRebinds': 0,
        'terminalState': 'sealed_ok', 'completionSequence': candidate['completionSequence']}
    exact(seal, [*seal_expected, 'finalSequence'], 'terminal_seal')
    require(all(equal(seal[k], v) for k, v in seal_expected.items()) and type(seal['finalSequence']) is int and
            candidate['completionSequence'] <= seal['finalSequence'] <= 9007199254740991, 'terminal_seal')
    records = sorted(oracle['users'] + oracle['reservedUnchangedDecoys'], key=lambda r: r['wireSlot'])
    require(type(candidate['pages']) is list and len(candidate['pages']) == 4, 'raw_transcript')
    wire_objects, ordinal, aggregate, done_total = [], 0, 0, 0
    previous_cookie = sha(b'')
    for page_idx, page in enumerate(candidate['pages'], 1):
        exact(page, 'pageIdx requestMessageID requestCookieSHA256 entries done'.split(), 'raw_transcript')
        require(equal(page['pageIdx'], page_idx) and equal(page['requestMessageID'], page_idx + 3) and
                page['requestCookieSHA256'] == previous_cookie, 'raw_transcript')
        require(type(page['entries']) is list and len(page['entries']) == (6 if page_idx == 4 else 15), 'raw_transcript')
        for entry in page['entries']:
            exact(entry, 'entryOrdinal messageID frameBase64 sha256'.split(), 'raw_transcript')
            require(equal(entry['entryOrdinal'], ordinal) and equal(entry['messageID'], page_idx + 3), 'raw_transcript')
            frame = b64(entry['frameBase64'], 2048, 'raw_transcript')
            require(sha(frame) == entry['sha256'], 'raw_transcript')
            mid, dn, values = entry_wire(frame)
            require(mid == page_idx + 3 and (dn, values) == literal_wire(records[ordinal]), 'raw_transcript')
            pub = decoded_public(dn, values)
            require(equal(pub, records[ordinal]['public12']), 'source_binding')
            wire_objects.append(stored_object(dn, values))
            aggregate += len(frame)
            ordinal += 1
        done = page['done']
        exact(done, 'redactedFrameBase64 cookieValueOffset cookieValueLength originalFrameSHA256 redactedFrameSHA256 cookieSHA256 requestCookieSHA256'.split(), 'raw_transcript')
        for key in ('originalFrameSHA256', 'redactedFrameSHA256', 'cookieSHA256', 'requestCookieSHA256'):
            schema(done[key], 'Hash', 'raw_transcript')
        require(done['requestCookieSHA256'] == page['requestCookieSHA256'], 'raw_transcript')
        frame = b64(done['redactedFrameBase64'], 4096, 'raw_transcript')
        done_wire(frame, done, page_idx + 3, page_idx)
        done_total += len(frame)
        aggregate += len(frame)
        previous_cookie = done['cookieSHA256']
    require(aggregate <= 128 << 10 and done_total <= 4096, 'raw_transcript')
    require(sorted(wire_objects, key=lambda x: x['base']['objectGUID']) ==
            sorted(source['objects'], key=lambda x: x['base']['objectGUID']), 'source_binding')
    return envelope


def validate_source(source, oracle):
    raw = b64(source['bodyBase64'], 256 << 10, 'source_binding')
    require(sha(raw) == source['sha256'], 'source_binding')
    decoded = strict_json(raw, 256 << 10, 'source_binding')
    schema(decoded, 'DecodedObservation', 'source_binding')
    require(instant(decoded['source']['completed_at']) >= instant(decoded['source']['started_at']), 'source_binding')
    records = oracle['users'] + oracle['reservedUnchangedDecoys']
    expected = [stored_object(*literal_wire(r)) for r in records]
    require(len({x['base']['objectGUID'] for x in decoded['objects']}) == 51 and
            sorted(decoded['objects'], key=lambda x: x['base']['objectGUID']) == sorted(expected, key=lambda x: x['base']['objectGUID']), 'source_binding')
    for r in records:
        require(equal(decoded_public(*literal_wire(r)), r['public12']), 'source_binding')
    payload = {'credentialSource': 'operation_account', 'connectionRevision': source['selection']['revision'],
        'connectionCredentialGeneration': source['selection']['credentialRevision'], **source['sourcePins'], 'dictionaryVersion': 3}
    require(equal(source['taskBinding']['payload'], payload) and source['taskBinding']['payloadHash'] == task_hash(payload), 'task_binding')
    return decoded


def query_for(source, mode, page):
    request = request_for(source, mode)
    query = {k: v for k, v in request.items() if k not in ('screenColumns', 'sAMAccountName', 'startTm', 'endTm')}
    if mode == 'omitted':
        query.pop('uacDisableBit')
    query.update(pageIdx=str(page), pageSize='10')
    return sorted([list(p) for p in query.items()])


def validate_public_task(task, task_id, domain_id, ready):
    require(task['taskUUID'] == task_id and task['domainId'] == domain_id, 'task_binding')
    state = task['state']
    require(task['sourceState'] == {'queued': 'PENDING', 'running': 'STARTED', 'succeeded': 'SUCCESS'}[state], 'task_binding')
    require(instant(task['updatedAt'], 'task_binding') >= instant(task['createdAt'], 'task_binding'), 'task_binding')
    if state == 'succeeded':
        require(task['attempt'] == 1 and task['progress'] == 100 and task['terminalAt'] is not None, 'task_binding')
        require(instant(task['terminalAt'], 'task_binding') >= instant(task['createdAt'], 'task_binding'), 'task_binding')
    else:
        require(task['terminalAt'] is None and task['progress'] < 100, 'task_binding')
    if ready:
        require(state == 'succeeded', 'task_binding')


def validate_download(download, task_id, actor, raw, artifact):
    filename = f'user-assets-v3-{task_id}.xlsx'
    require(download['filename'] == filename and artifact == {'rowCount': 11, 'byteCount': len(raw), 'sha256': sha(raw), 'filename': filename}, 'artifact_binding')
    expected = {'x-adtr-user-id': str(actor), 'cache-control': 'no-store', 'x-content-type-options': 'nosniff',
        'content-type': 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet', 'content-length': str(len(raw)),
        'x-adtr-artifact-sha256': sha(raw), 'x-adtr-row-count': '11', 'content-disposition': f'attachment; filename="{filename}"'}
    require(download['headers'] == expected, 'artifact_binding')


def validate_uac_evidence(data, read_artifact, provenance, *, oracle):
    """Validate the complete final envelope. Success is None, never a success flag."""
    try:
        scalar_tree(data)
        schema(data, 'FinalEvidence')
        schema(provenance, 'Provenance', 'provenance')
        require(equal(data['provenance'], provenance), 'provenance')
        require(oracle.get('profile') == PROFILE and oracle.get('format') == 1, 'source_binding')
        source = data['source']
        decoded = validate_source(source, oracle)
        require(str(data['actorId']) != source['actorId'], 'source_binding')
        budget = data['sensitiveBudget']
        before = {'owner': 7, 'producer': 9, 'observations': 1, 'directoryUses': 1, 'exportTasks': 0}
        after_each = [{**before, 'owner': 8 + i, 'exportTasks': i + 1} for i in range(3)]
        require(equal(budget, {'before': before, 'afterEach': after_each, 'after': after_each[-1]}), 'post_count')
        require(equal(data['postLedger']['finalSensitiveSnapshot'], after_each[-1]), 'post_count')
        pages = data['listPages']
        expected_pages = [(mode, i) for mode in ('omitted', *MODES) for i in range(1, 5 if mode == 'omitted' else 3)]
        require([(p['mode'], p['pageIdx']) for p in pages] == expected_pages, 'source_binding')
        for page in pages:
            mode, idx, response = page['mode'], page['pageIdx'], page['response']
            require(sorted(page['queryEntries']) == query_for(source, mode, idx), 'source_binding')
            literal = oracle['queries'][mode]
            require(equal(response['list'], literal['pages'][idx - 1]['public12Rows']), 'source_binding')
            require(response['selection'] == source['selection'] and response['observationId'] == source['taskUUID'] and
                    response['source'] == decoded['source'] and response['page'] == {'pageIdx': idx, 'pageSize': 10, 'total': literal['total'], 'totalPage': literal['totalPage']}, 'source_binding')
        require([c['name'] for c in data['cases']] == list(MODES), 'artifact_binding')
        require([p['case'] for p in data['postLedger']['events']] == list(MODES), 'post_binding')
        ids, idem_hashes = set(), set()
        for index, case in enumerate(data['cases']):
            mode, task_id = case['name'], case['create']['taskID']
            request = request_for(source, mode)
            post = data['postLedger']['events'][index]
            require(task_id not in ids and post['idempotencyKeySHA256'] not in idem_hashes, 'post_binding')
            ids.add(task_id)
            idem_hashes.add(post['idempotencyKeySHA256'])
            require(equal(case['request'], request) and post['ordinal'] == index + 1 and post['taskID'] == task_id and
                    equal(post['safeBusinessRequest'], request) and post['sanitizedRequestSHA256'] == safe_request_hash(request), 'post_binding')
            require(sorted(case['listQueryEntries']) == query_for(source, mode, 2), 'source_binding')
            for response, ready in [(case['create'], False), (case['receipt'], True), (case['detail'], True), (case['originalTaskReadback']['detail'], True)]:
                require(response['taskID'] == task_id and equal(response['request'], request), 'task_binding')
                validate_public_task(response['task'], task_id, request['domainId'], ready)
            filename = f'user-assets-v3-{task_id}.xlsx'
            raw = read_artifact(filename)
            require(type(raw) is bytes and 0 < len(raw) <= 32 << 20, 'artifact_binding')
            artifact = case['detail']['artifact']
            validate_download(case['download'], task_id, data['actorId'], raw, artifact)
            original = case['originalTaskReadback']
            require(original['detail']['artifact'] == artifact and original['downloadSHA256'] == sha(raw) and original['downloadByteCount'] == len(raw), 'artifact_binding')
            validate_download(original['download'], task_id, data['actorId'], raw, artifact)
            require(workbook_rows(raw) == oracle['queries'][mode]['expectedWorkbookRows'], 'artifact_binding')
            stored = case['database']
            require(stored['taskID'] == task_id and stored['actorId'] == str(data['actorId']) and stored['sourceSHA256'] == source['sha256'] and
                    stored['byteCount'] == len(raw) and stored['sha256'] == sha(raw) and stored['idempotencyKeySHA256'] == post['idempotencyKeySHA256'], 'task_binding')
            require(equal(stored['taskBinding']['payload'], request) and stored['taskBinding']['payloadHash'] == task_hash(request), 'task_binding')
            chunks = stored['chunks']
            require(stored['chunkCount'] == len(chunks), 'artifact_binding')
            joined = bytearray()
            for ordinal, chunk in enumerate(chunks):
                blob = b64(chunk['bodyBase64'], 1 << 20)
                require(chunk['ordinal'] == ordinal and len(blob) > 0 and sha(blob) == chunk['sha256'], 'artifact_binding')
                joined.extend(blob)
            require(bytes(joined) == raw, 'artifact_binding')
            events = stored['events']
            require({'submitted', 'leased', 'started', 'progress', 'finished'} <= {e['action'] for e in events} and
                    events[-1]['state'] == 'succeeded' and events[-1]['action'] == 'finished' and
                    [e['resultVersion'] for e in events] == sorted(e['resultVersion'] for e in events), 'task_binding')
            picture = case['screenshot']
            require(picture['filename'] == f'{mode}-ready.png', 'artifact_binding')
            png = read_artifact(picture['filename'])
            require(type(png) is bytes and sha(png) == picture['sha256'], 'artifact_binding')
            validate_png(png)
        raw = read_artifact(RAW_NAME)
        require(type(raw) is bytes and sha(raw) == data['rawWire']['sha256'], 'raw_transcript')
        envelope = validate_wire(raw, decoded, oracle, data['cleanupReceipt']['nonce'])
        require(envelope['candidateTranscriptSHA256'] == data['rawWire']['candidateTranscriptSHA256'] and
                envelope['terminalSealSHA256'] == data['rawWire']['terminalSealSHA256'], 'raw_transcript')
    except UACEvidenceError:
        raise
    except Exception:
        raise UACEvidenceError('candidate_schema') from None


def load_oracle(repo):
    fd = None
    try:
        path = Path(os.path.abspath(Path(repo) / 'scripts/fixtures/user_uac_v3_mixed33_oracle.json'))
        require(not any(p.is_symlink() for p in (path, *path.parents)), 'source_binding')
        metadata = path.lstat()
        require(stat.S_ISREG(metadata.st_mode) and 0 < metadata.st_size <= 1 << 20, 'source_binding')
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
        opened = os.fstat(fd)
        require(stat.S_ISREG(opened.st_mode) and (opened.st_dev, opened.st_ino) == (metadata.st_dev, metadata.st_ino), 'source_binding')
        chunks, count = [], 0
        while True:
            chunk = os.read(fd, min(65536, (1 << 20) + 1 - count))
            if not chunk:
                break
            chunks.append(chunk)
            count += len(chunk)
            require(count <= 1 << 20, 'source_binding')
        raw = b''.join(chunks)
        require(len(raw) == opened.st_size and sha(raw) == ORACLE_SHA256, 'source_binding')
        return strict_json(raw, 1 << 20, 'source_binding')
    except Exception:
        raise UACEvidenceError('source_binding') from None
    finally:
        if fd is not None:
            os.close(fd)
