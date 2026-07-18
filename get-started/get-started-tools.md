# Getting Started with the Tools Registry

This guide provides step-by-step instructions to use the Tools Registry to create, retrieve, update, and manage version lifecycles for your custom AI and agent tools.

The Tools Registry enables you to store and manage tool definitions, specifying the exact input parameters and expected output structure as JSON Schema specifications. This metadata allows LLM orchestration systems to discover and execute your tools dynamically. Unlike prompts, versions in the Tools Registry manage the evolution of these input and output schemas.

## Prerequisites

Before starting, ensure you have completed the following setup:

1. Spun up the local services with `docker compose up -d` (see [Getting Started Guide](get-started.md)).
2. Registered, verified, and logged in to obtain an Access Token (`sess_...`) or created a programmatic API Key (`ak_...`).
3. Retrieved your `PX0_PROJECT_ID` (see [Getting Started Guide](get-started.md#1-retrieve-your-organization-and-team-id)).

```bash
export PX0_ACCESS_TOKEN=<your_session_token_or_api_key>
export PX0_PROJECT_ID=<your_project_id>
```

## Step 1: Create a Tool

You can create a tool inside your project by sending a POST request containing the name, slug, and description of the tool.

```bash
curl -i -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/tools \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Weather Fetcher", "slug": "weather", "description": "Retrieve current weather information for a given location"}'
```

Copy the tool ID from the JSON response to use in subsequent requests:

```bash
export PX0_TOOL_ID=<tool_id>
```

## Step 2: Create a Tool Version

Once you have created a tool, initialize a version to define its input and output interfaces. Creating a version establishes a draft with optional input and output JSON Schema specifications.

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"input_schema": {"type": "object", "properties": {"location": {"type": "string", "description": "The city and state, e.g. San Francisco, CA"}, "unit": {"type": "string", "enum": ["celsius", "fahrenheit"], "default": "celsius"}}, "required": ["location"]}, "output_schema": {"type": "object", "properties": {"temperature": {"type": "number"}, "conditions": {"type": "string"}}, "required": ["temperature", "conditions"]}}'
```

Copy the tool version number from the JSON response to use in subsequent requests:

```bash
export PX0_TOOL_VERSION=1
```

## Step 3: Retrieve and Update Tool Versions
### 1. List versions for a tool
Retrieve metadata for all versions associated with a specific tool:

```bash
curl -i -X GET http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 2. Retrieve a specific version
Query the detailed schema information of a specific tool version:

```bash
curl -i -X GET http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION} \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 3. Update schemas of a draft version
Modify the input and output schemas of your active draft version:

```bash
curl -i -X PUT http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION} \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"input_schema": {"type": "object", "properties": {"location": {"type": "string"}}}, "output_schema": {"type": "object", "properties": {"temp": {"type": "number"}}}}'
```

## Step 4: Versioning, Branching, and Lifecycles
### 1. Branch/Duplicate a Version
Duplicate the schemas of an existing version to create a new draft version. This increments the version number and populates the schemas from the source version:

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION}/duplicate \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 2. Promote Version Status
Move your version along the standard lifecycle pipeline: draft -> stable -> live. Once a version is promoted beyond draft, its schemas are immutable and cannot be updated.

Promote your version to stable:

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION}/promote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

Promote your version to live (this automatically demotes any previously live version of this tool to stable):

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION}/promote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 3. Demote or Archive a Version
Manage the rollback or cleanup of tool versions.

Demote a version from live back to stable:

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION}/demote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

Archive a version to mark it as inactive:

```bash
curl -i -X POST http://localhost:8000/v1/tools/${PX0_TOOL_ID}/versions/${PX0_TOOL_VERSION}/archive \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

## Step 5: Listing and Filtering Tools
### 1. List tools inside a project
Retrieve all tools created within your specified project:

```bash
curl -i -X GET http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/tools \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 2. List tools globally with filtering
Filter tools globally using either the `project` or the `project_id` query parameter:

```bash
curl -i -X GET "http://localhost:8000/v1/tools?project=${PX0_PROJECT_ID}" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

## Summary of REST Endpoints

| HTTP Method | Route | Description | Role Requirement |
| ----------- | ---------------------------------------------- | ---------------------------------------------- | ----------------------- |
| `POST`      | `/v1/projects/:projectID/tools`                | Create a tool                                  | Editor / Admin          |
| `GET`       | `/v1/projects/:projectID/tools`                | List tools in a project                        | Viewer / Editor / Admin |
| `GET`       | `/v1/tools`                                    | List tools globally with filtering             | Viewer / Editor / Admin |
| `GET`       | `/v1/tools/:id`                                | Get tool metadata (ID or slug)                 | Viewer / Editor / Admin |
| `PUT`       | `/v1/tools/:id`                                | Update tool metadata (ID or slug)              | Editor / Admin          |
| `DELETE`    | `/v1/tools/:id`                                | Delete tool and all its versions               | Editor / Admin          |
| `GET`       | `/v1/tools/:id/versions`                        | List versions for a tool                       | Viewer / Editor / Admin |
| `POST`      | `/v1/tools/:id/versions`                        | Create an empty draft version                  | Editor / Admin          |
| `GET`       | `/v1/tools/:id/versions/:version`              | Get version details                            | Viewer / Editor / Admin |
| `PUT`       | `/v1/tools/:id/versions/:version`              | Update draft version schemas                   | Editor / Admin          |
| `DELETE`    | `/v1/tools/:id/versions/:version`              | Delete or archive version                      | Editor / Admin          |
| `POST`      | `/v1/tools/:id/versions/:version/promote`      | Promote version (draft -> stable -> live)      | Editor / Admin          |
| `POST`      | `/v1/tools/:id/versions/:version/demote`       | Demote version (live -> stable)                | Editor / Admin          |
| `POST`      | `/v1/tools/:id/versions/:version/archive`      | Archive version                                | Editor / Admin          |
| `POST`      | `/v1/tools/:id/versions/:version/duplicate`    | Duplicate version schemas to new draft         | Editor / Admin          |
