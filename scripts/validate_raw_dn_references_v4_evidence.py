"""Mandatory reverse-DN slice of the real primary manifest, never standalone proof."""
import base64
import hashlib
import json
import re

PATH = '/api/directory-assets/v4/raw-dn-references'


def validate(data, b, old_ui, read_screenshot, prefix, require, exact, display, validate_png):
    exact(data, ['actorId', 'authorization', 'observationId', 'cases', 'writes', 'navigationCleared', 'logoutCleared'], 'raw reference UI fields')
    require(type(data['actorId']) is int and data['actorId'] == old_ui['actorId'] and data['authorization'] == old_ui['authorization'], 'raw reference actor/authority')
    require(data['observationId'] == b['taskUUID'] and data['writes'] == [] and data['navigationCleared'] is True and data['logoutCleared'] is True, 'raw reference fixed read-only lifecycle')
    # Recompute from original storage bytes, not result.list or a relationship table.
    body = json.loads(base64.b64decode(b['bodyBase64'], validate=True))
    groups = [o for o in body['objects'] if o['base']['kind'] == 'group']
    returned = [g for g in groups if g['membership']['memberBytes'] is not None]
    coverage = {'groupCount': len(groups), 'memberReturnedGroupCount': len(returned), 'memberNotReturnedGroupCount': len(groups) - len(returned)}
    require(coverage == {'groupCount': 12, 'memberReturnedGroupCount': 11, 'memberNotReturnedGroupCount': 1}, 'raw reference fixture coverage')
    require(type(data['cases']) is list and len(data['cases']) == 6, 'six reverse-reference desktop/mobile cases')
    selection = old_ui['cases'][0]['reply']['value']['selection']
    for case, (width, height, index) in zip(data['cases'], [(w, h, i) for w, h in [(1440, 900), (390, 844)] for i in [0, 13, 24]]):
        exact(case, ['viewport', 'reply', 'reread', 'replacement', 'restored', 'reopened', 'rendered', 'closed', 'screenshot'], 'raw reference case fields')
        require(case['viewport'] == {'width': width, 'height': height} and case['closed'] is True, 'raw reference viewport/close')
        obj = b['objects'][index]
        dn = body['objects'][index]['base']['distinguishedName']
        matched = sorted(g['base']['objectGUID'] for g in returned if dn in [base64.b64decode(v, validate=True).decode('utf8') for v in g['membership']['memberBytes']])
        require(matched == ([] if index == 24 else [b['objects'][14]['objectGUID']]), 'raw reference positive and zero oracle')
        result = [next(o for o in b['objects'] if o['objectGUID'] == guid) for guid in matched]
        expected_query = {'domainId': selection['domainId'], 'expectedRevision': selection['revision'], 'expectedCredentialRevision': selection['credentialRevision'], 'observationId': b['taskUUID'], 'objectGUID': obj['objectGUID'], 'pageIdx': '1', 'pageSize': '25'}
        expected_value = {'dictionaryVersion': 4, 'selection': selection, 'observationId': b['taskUUID'], 'source': b['source'], 'object': obj, 'list': result, 'page': {'pageIdx': 1, 'pageSize': 25, 'total': len(result), 'totalPage': 1 if result else 0}, 'coverage': coverage}
        replacement_obj = b['objects'][index + 1]
        replacement_dn = body['objects'][index + 1]['base']['distinguishedName']
        require(not any(replacement_dn in [base64.b64decode(v, validate=True).decode('utf8') for v in g['membership']['memberBytes']] for g in returned), 'replacement zero oracle')
        for name in ['reply', 'reread', 'replacement', 'restored', 'reopened']:
            reply = case[name]
            query_expected = {**expected_query, 'objectGUID': replacement_obj['objectGUID']} if name == 'replacement' else expected_query
            value_expected = {**expected_value, 'object': replacement_obj, 'list': [], 'page': {'pageIdx': 1, 'pageSize': 25, 'total': 0, 'totalPage': 0}} if name == 'replacement' else expected_value
            exact(reply, ['query', 'value', 'witness'], 'raw reference captured response')
            query = reply['query']
            require(type(query) is list and len(query) == 7 and all(type(pair) is list and len(pair) == 2 and all(type(v) is str for v in pair) for pair in query), 'raw reference exact query entries')
            require(dict(query) == query_expected and len(dict(query)) == 7, 'raw reference exact query pins')
            require(json.dumps(reply['value'], sort_keys=True, ensure_ascii=False) == json.dumps(value_expected, sort_keys=True, ensure_ascii=False), 'raw reference stored oracle response')
            w = reply['witness']
            exact(w, ['byteLength', 'sha256', 'state', 'uncanceledEOF', 'eof', 'status', 'actor', 'cache', 'readers', 'released', 'signalAborted', 'cancelCalls', 'method', 'path'], 'raw reference native witness')
            require(w['method'] == 'GET' and w['path'] == PATH and type(w['status']) is int and w['status'] == 200 and w['actor'] == str(data['actorId']) and w['cache'] == 'no-store', 'raw reference actor-bound GET')
            require(w['state'] == 'complete' and w['uncanceledEOF'] is True and w['eof'] is True and w['signalAborted'] is False and type(w['cancelCalls']) is int and w['cancelCalls'] == 0, 'raw reference uncanceled EOF')
            require(type(w['readers']) is int and w['readers'] == 1 and type(w['released']) is int and w['released'] == 1, 'raw reference one released reader')
            require(type(w['byteLength']) is int and 0 < w['byteLength'] <= 8388608 and type(w['sha256']) is str and re.fullmatch('[0-9a-f]{64}', w['sha256']), 'raw reference native body bounds/hash')
        require(case['reply']['query'] == case['reread']['query'] and case['reply']['witness']['sha256'] == case['reread']['witness']['sha256'] and case['reply']['witness']['byteLength'] == case['reread']['witness']['byteLength'], 'raw reference pinned reread bytes')
        require(case['rendered'] == [f"{display(g['distinguishedName'])} · {g['objectGUID']}" for g in result], 'raw reference escaped rendered groups')
        shot = case['screenshot']
        exact(shot, ['filename', 'sha256'], 'raw reference screenshot fields')
        require(shot['filename'] == f'group-members-v4-raw-dn-{prefix}{width}-{index}.png', 'raw reference expected screenshot name')
        png = read_screenshot(shot['filename'])
        validate_png(png, width, height)
        require(hashlib.sha256(png).hexdigest() == shot['sha256'], 'raw reference screenshot hash')
