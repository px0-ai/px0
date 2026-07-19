# Getting Started with Search Integration

This guide provides step-by-step instructions to use the Registry Search API to query your versioned prompts, skills, and tools across your project boundaries.

The search engine executes hybrid retrieval by combining full-text search (FTS) with vector search to rank candidates using reciprocal-rank fusion. The default configuration uses OpenSearch for FTS lexical queries and filters results to enforce role-based access control (RBAC) scopes.

---

## Prerequisites

Before starting, ensure you have:

- Spun up the local services with `docker compose up -d`
- Registered, verified, and logged in to obtain an Access Token (`sess_...`)
- Retrieved your `PX0_PROJECT_ID`

```bash
export PX0_ACCESS_TOKEN=<your_session_token>
export PX0_PROJECT_ID=<your_project_id>
```

---

## Step 1: Create Searchable Test Data

To verify search, create a mixture of prompts, skills, and tools under your active project:

### 1. Create a Prompt

```bash
curl -s -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/prompts \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Refund Policy Agent", "slug": "refund-prompt", "description": "Handles general refund and cancellation policy inquiries"}'
```

### 2. Create a Skill

```bash
curl -s -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/skills \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Refund Calculator Script", "slug": "refund-skill", "description": "Custom code script to compute refund values and processing fees"}'
```

### 3. Create a Tool

```bash
curl -s -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/tools \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Refund Processor Tool", "slug": "refund-tool", "description": "Triggers database and ledger updates to process refunds"}'
```

---

## Step 2: Query the Search API

Send a natural-language search request to the central query endpoint. You must supply your query via the `q` query parameter:

```bash
curl -i -X GET "http://localhost:8000/v1/search?q=refund" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### Sample Response

The search engine returns a prioritized list of matching references from all supported entity types:

```json
{
  "results": [
    {
      "id": "e67e3bf1-893a-4db5-b82c-473cd98f62c0",
      "type": "prompt",
      "slug": "refund-prompt",
      "name": "Refund Policy Agent",
      "description": "Handles general refund and cancellation policy inquiries"
    },
    {
      "id": "a98c39e2-2a2d-4ffd-89be-0c2d3a9bb73c",
      "type": "skill",
      "slug": "refund-skill",
      "name": "Refund Calculator Script",
      "description": "Custom code script to compute refund values and processing fees"
    },
    {
      "id": "189fd7bc-637a-4c22-b9cf-89ae21cd03bf",
      "type": "tool",
      "slug": "refund-tool",
      "name": "Refund Processor Tool",
      "description": "Triggers database and ledger updates to process refunds"
    }
  ]
}
```

---

## Step 3: Filtering Search Results

You can restrict search results to a specific entity type by supplying the optional `type` parameter:

### Filter by Prompt

```bash
curl -i -X GET "http://localhost:8000/v1/search?q=refund&type=prompt" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### Filter by Skill

```bash
curl -i -X GET "http://localhost:8000/v1/search?q=refund&type=skill" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### Filter by Tool

```bash
curl -i -X GET "http://localhost:8000/v1/search?q=refund&type=tool" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

---

## Step 4: Access Control and Scoping

The search endpoint automatically filters candidates at the provider boundaries to prevent data leaks.

### Reciprocal-Rank Fusion and Hydration

Let $R(e)$ denote the reciprocal-rank fusion score of an entity $e$ across lexical and semantic retrievers:

$$
S(e) = \sum_{m \in M} \frac{1}{k + r_m(e)}
$$

where $M$ represents the set of active search retrievers, $r_m(e)$ is the rank of entity $e$ returned by retriever $m$, and $k$ is a constant default of 60.

After computing the fused ranks, the engine queries the PostgreSQL database using an explicit project-scoping check:

- The search engine only retrieves records belonging to projects that the requesting user or API key has explicit permissions to access.
- Any entity returned by an external search provider that is outside the user scope is automatically excluded during hydration.
- Archived prompts are excluded from search results automatically.

---

## Summary of Search Endpoint

| HTTP Method | Route | Description | Role Requirement |
| ----------- | ------------ | --------------------------------------------- | ----------------------- |
| `GET`       | `/v1/search` | Search for prompts, skills, and tools by query | Viewer / Editor / Admin |
