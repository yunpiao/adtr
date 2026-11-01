# Simulated group relationship contract v1

## Evidence status and purpose

`internal/groupgraph` is a callable, pure Go relationship engine for **explicitly
synthetic** inputs. The working assumption contract is
`adtr.simulated-groupgraph.v1`; `simulated: true` is required. This is an engineering
slice for AD-F-043 (#30, viewing members/member-of) and AD-F-044 (#29, relationship
analysis), tracked with the simulation batch #251. It is not a recovered source
system specification or product acceptance evidence. Product acceptance remains
0/209. No effective ACL/authorization evaluation is implemented.

Verified facts: dictionary4 stores bounded raw member values without DN resolution,
primary-group supplementation, recursive expansion or complete memberOf semantics.
This package does not silently reinterpret those raw values or upgrade a stored
observation. Its input is a separately supplied graph of opaque synthetic IDs.
Whether these semantics match the original product remains unverified; a future
real-data adapter needs reviewed identity resolution, observation consistency,
source completeness, domain isolation and current caller authorization contracts.

## API and assumed semantics

- `Build(Snapshot) (*Graph, error)` validates and copies an immutable snapshot.
  Snapshot requires the exact version, simulation marker, nonempty tenant/domain
  scope and observation ID. Nodes have unique IDs and one of `user`, `group`,
  `computer`. IDs are exact case-sensitive UTF-8 strings, 1–256 bytes, without
  controls or surrounding whitespace. No DN, SID or Unicode normalization occurs.
- A group's `membersKnown: true` means the fixture supplies its explicit outgoing
  member list (possibly empty). `false` means unobserved membership, not an empty
  group. Non-groups must use false. An edge's source must be a known group whose
  membership is known. Duplicate nodes/edges and invalid kinds are rejected.
- Edges are `groupId -> memberId`. An absent target is preserved as an explicit
  unknown-reference diagnostic; it does not create a node, trigger a lookup or
  silently become a confirmed relationship. An absent source is rejected.
- `Graph.Query(Query)` supports `members` (outgoing) and `member_of` (incoming),
  direct or transitive, under an exactly matching scope. This equality guard is
  not caller authentication: the caller must independently authorize access.
  `members` requires a group; member-of permits any existing object kind.
- BFS yields unique relationships with shortest hop `distance` and `direct` true
  exactly when distance is 1. Results sort by target ID. Cycles never enumerate
  unbounded paths. The query root is always excluded, including self-membership;
  a self-loop still appears in cycle diagnostics. No effective privilege,
  tokenGroups, primary-group or cross-domain expansion is inferred.
- `Graph.Diagnostics()` returns sorted unknown edges and sorted cyclic strongly
  connected components of groups. A self-loop is a singleton cycle; a diamond
  without a back edge is not a cycle. Cycles are permitted and reported rather
  than rejecting the whole snapshot. These diagnostics cover the **whole** supplied
  graph, not only the queried component.
- Query `unknownReferences` contains unknown targets encountered during forward
  traversal. For a transitive forward query, any reached group with an unknown
  outgoing list is listed in `unknownMembershipGroups`; a direct query inspects
  only the root's outgoing list. For reverse queries, **every** group with an
  unknown outgoing list is listed: such a group could contain the requested
  object even if no known edge reaches it. Known references to absent nodes do
  not independently imply missing incoming edges to a different known object.
- `completeWithinSnapshot` means the requested traversal has no such unresolved
  inputs within this explicit synthetic graph. It never means AD completeness,
  complete effective permissions or source-system compatibility. A cycle alone
  does not prevent complete reachability over supplied edges.
- Results carry version, simulated marker, scope and observation ID. All returned
  collections are independently owned. The built graph is safe for concurrent
  read-only calls; no caller slices are retained. Errors are fixed strings and
  never echo supplied IDs or raw data.

## Required protection parameters and effects

| Parameter | Requirement / problem solved | Removal or tightening effects |
| --- | --- | --- |
| `version`, `simulated` | Required; prevent presenting an assumed synthetic contract as verified production behavior | Omitting/mismatching rejects the input; removing validation loses provenance separation |
| `scope`, `observationId` | Required nonempty provenance; query scope must exactly match | Removing allows accidental scope/observation mixing; this does not replace runtime authorization |
| `membersKnown` | Required semantic distinction in the typed contract; omitted JSON bool means false/unknown, never known empty | Treating false as empty would create false negative membership claims |
| Build `MaxNodes=4096`, `MaxEdges=16384`, ID bytes 256 | Fixed local input/memory/SCC protection limits, not capacity promises | Removing permits resource exhaustion; larger otherwise legitimate graphs are rejected |
| Query `maxDepth` | Required 1–64; bounds transitive distance | Removing allows unexpectedly deep traversal; tighter bounds can reject longer reachable chains |
| Query `maxResults` | Required 1–4096; bounds returned distinct known targets | Removing allows excessive output; tighter limits can reject broad memberships |

Unknown targets are bounded by the edge limit, and unknown-membership diagnostics
by the node limit. Graph creation and SCC detection are O(nodes + edges), apart
from deterministic sorting. Traversal visits each reached node at most once;
there is no all-path enumeration. A limit failure returns a zero `Result` and
`ErrLimit`, never a truncated successful result. Input size is checked before
building maps; transport-level byte limits remain the consuming application's
responsibility. Direct queries still require valid budgets. Depth counts edges;
revisits of already discovered nodes do not consume further depth or results.

## Minimal synthetic call

```go
snapshot := groupgraph.Snapshot{
    Version: groupgraph.ContractVersion, Simulated: true,
    Scope: groupgraph.Scope{TenantID: "synthetic-tenant", DomainID: "synthetic-domain"},
    ObservationID: "synthetic-observation",
    Nodes: []groupgraph.Node{
        {ID: "g", Kind: groupgraph.Group, MembersKnown: true},
        {ID: "u", Kind: groupgraph.User},
    },
    Edges: []groupgraph.Edge{{GroupID: "g", MemberID: "u"}},
}
graph, err := groupgraph.Build(snapshot)
if err != nil { /* reject input */ }
result, err := graph.Query(groupgraph.Query{
    Scope: snapshot.Scope, ObjectID: "u", Direction: groupgraph.MemberOf,
    Transitive: true, MaxDepth: 16, MaxResults: 256,
})
// result.Relationships contains g, distance 1, direct true.
```

## Verification and remaining gates

Baseline: `go test ./internal/directoryassets` passed before implementation.
Focused verification: `go test -race -cover ./internal/groupgraph` and
`go vet ./internal/groupgraph` passed. Tests cover independent expected forward
and reverse memberships, shortest distances, unknown/empty distinction, cycles,
self-loops, diamond deduplication, order independence, scope mismatch, malformed
inputs, exact and exceeded budgets, ownership, concurrent reads and a 4096-node
cycle. These are synthetic unit tests, not database, browser, LDAP or AD evidence.

No production API, store migration, permission grant, collector, remote action,
network access or credentials are added by this package. Full aggregate checks,
independent review, exact-head CI and main compatibility remain integration gates.
Real AD, original product semantics, Windows variants and target capacity remain
unverified. Do not mark either feature product-accepted based on these tests.
