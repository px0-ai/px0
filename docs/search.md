# Search architecture

px0 exposes registry search through `GET /v1/search`. Clients always send a
natural-language `q` value and may restrict results with `type=prompt`,
`type=skill`, or `type=tool`. Without `type`, the request searches every
registered entity type. Provider names, embedding vectors, and raw relevance
scores remain internal so the API does not change when infrastructure changes.

## Retrieval and ranking

The search engine sends the same authorization-scoped request to two
independent retriever slots:

- the FTS retriever finds lexical matches;
- the vector retriever finds semantic matches.

Both retrievers return ordered entity references. Reciprocal-rank fusion (RRF)
combines their ranks rather than comparing provider-specific scores, whose
scales are not compatible. The fused references are hydrated through a second
project-scoped database query. This defense-in-depth check prevents a remote or
misconfigured provider from exposing an entity outside the requester's RBAC
scope.

## PostgreSQL full-text indexes

Migration `025_search.sql` adds a stored generated `tsvector` column to the
`prompts`, `skills`, and `tools` tables. Each document uses the English text
search configuration and assigns these weights:

| Field | Weight | Rationale |
| --- | --- | --- |
| `name` | A | A direct name match is usually the strongest expression of intent. |
| `description` | B | Descriptions contain useful natural-language context but are broader. |
| `slug` | C | Slugs are useful exact identifiers but often contain abbreviated tokens. |

Generated columns were chosen instead of application-managed index rows or
triggers because PostgreSQL recomputes them in the same transaction as every
insert or update. Search cannot become stale when metadata changes, and no
queue or backfill worker is required.

Each generated column has a GIN index. GIN is designed for inverted membership
lookups such as `tsvector @@ tsquery` and avoids scanning every registry row.
Queries use `websearch_to_tsquery('english', q)` so ordinary user-entered text
is parsed safely, and `ts_rank_cd` orders matching candidates before RRF.
Archived prompts are excluded; skills and tools currently have no container
archive state.

## Adding another searchable entity

To add a registry entity without changing the public endpoint:

1. Add its singular value to `model.SearchEntityType` and the OpenAPI `type`
   enum.
2. Add a generated search document and GIN index in a new migration.
3. Add the entity's authorization-scoped query to each implemented retriever.
4. Add its hydration query to `store.GetSearchResults`.
5. Cover unfiltered search, type-filtered search, updates, and RBAC isolation.

## Adding a provider

The px0 codebase supports a variety of search providers out of the box, configured via environment variables.

### Lexical Providers (`SEARCH_FTS_PROVIDER`)
* `postgres` (default)
* `elasticsearch`
* `opensearch`
* `algolia`

### Semantic Providers (`SEARCH_VECTOR_PROVIDER`)
* `none` (default)
* `qdrant`
* `pinecone`

Implement the `search.Retriever` interface and wire the provider name in
`internal/search/config.go`. Every implementation receives the permitted
project IDs and requested entity types and must apply both filters at the
provider boundary. Vector stores should persist `project_id` and `type` as
filterable metadata alongside each embedding. Keep credentials in environment
variables and fail startup on incomplete configuration rather than silently
falling back to a different provider.
