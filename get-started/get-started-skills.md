# Getting Started with the Skills Registry

This guide provides step-by-step instructions to use the **Skills Registry** to create, upload, retrieve, edit individual files, and manage version lifecycles for your custom AI/agent skills.

The Skills Registry enables you to store entire custom packages of code, metadata, or configurations (e.g. agent scripts, utility modules, schemas) directly in the database. Unlike prompts, versions in the Skills Registry are applied at the **skill level** (as snapshots of files) rather than the individual file level.

---

## Prerequisites

Before starting, ensure you have:
1. Spun up the local services with `docker compose up -d` (see [Getting Started Guide](get-started.md)).
2. Registered, verified, and logged in to obtain an **Access Token** (`sess_...`) or created a programmatic **API Key** (`ak_...`).
3. Retrieved your `PX0_PROJECT_ID` (see [Getting Started Guide](get-started.md#1-retrieve-your-organization-and-team-id)).

```bash
export PX0_ACCESS_TOKEN=<your_session_token_or_api_key>
export PX0_PROJECT_ID=<your_project_id>
```

---

## Step 1: Create a Skill

You can create a skill inside your project in two ways:

### Option A: Create an empty skill with JSON
This initializes the skill with version 1 as an empty `draft`:

```bash
curl -i -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/skills \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Calculator Agent", "slug": "calculator", "description": "A skill to perform arithmetic calculations"}'
```

Copy the skill's ID from the JSON response:
```bash
export PX0_SKILL_ID=<skill_id>
```

### Option B: Create a skill and upload a ZIP package at the same time
This creates the skill, initializes version 1, and automatically unzips and saves the contents of your ZIP file to the database.

Create a simple ZIP file locally:
```bash
echo "exports.add = (a, b) => a + b;" > math.js
echo '{"name": "math"}' > package.json
zip -r math-skill.zip math.js package.json
```

Upload using `multipart/form-data`:
```bash
curl -i -X POST http://localhost:8000/v1/projects/${PX0_PROJECT_ID}/skills \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -F "name=Calculator Agent" \
  -F "slug=calculator" \
  -F "description=A skill to perform arithmetic calculations" \
  -F "file=@math-skill.zip"
```

---

## Step 2: Upload a ZIP to a Draft Version

If you created your skill as empty (or want to overwrite all files in a draft version), you can upload a ZIP archive directly to the targeted draft version:

Create another zip with new/updated files:
```bash
echo "const sub = (a, b) => a - b;" > sub.js
zip -r calculator-v1.zip sub.js
```

Upload the ZIP file to overwrite all files in Draft Version 1:
```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/upload \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -F "file=@calculator-v1.zip"
```

---

## Step 3: Retrieve and Edit Individual Files

Since versioning is at the skill level, your draft version serves as an active workspace where you can inspect and modify individual files.

### 1. List files in a version
This returns metadata of all files (paths, sizes, created/updated timestamps), omitting the raw byte payload:

```bash
curl -i -X GET http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/files \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 2. Retrieve individual file content
Query the raw file content of an individual file by path:

```bash
curl -i -X GET "http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/files/content?file_path=sub.js" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 3. Upsert or edit an individual file
You can add a new file or update an existing one in the draft version:

```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/files \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"file_path": "config.json", "content": "{\"debug\": true}"}'
```

### 4. Delete an individual file
Delete a specific file from your draft workspace:

```bash
curl -i -X DELETE "http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/files?file_path=sub.js" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

---

## Step 4: Versioning, Branching, and Lifecycles

When your skill files are ready, you can publish them or create a new draft to continue iterating.

### 1. Branch/Duplicate a Version
This automatically increments the skill's version (creating version 2 as a `draft`) and copies all files from the source version to the new version workspace:

```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/duplicate \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 2. Promote Version Status
Move your version along the standard lifecycle pipeline: `draft` $\rightarrow$ `stable` $\rightarrow$ `live`.
*(Note: Once promoted beyond `draft`, files are immutable and cannot be updated/uploaded/deleted).*

Promote Version 1 to `stable`:
```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/promote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

Promote Version 1 to `live` (demotes any previous live version of this skill to `stable`):
```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/promote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

### 3. Demote or Archive a Version
If you need to archive or roll back a live version:

Demote Version 1 from `live` to `stable`:
```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/demote \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

Archive Version 1:
```bash
curl -i -X POST http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/archive \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}"
```

---

## Step 5: Download a Skill as a ZIP Package

At any point in the lifecycle, you can fetch the entire file structure of a specific version as a self-contained ZIP archive:

```bash
curl -L -o downloaded-skill.zip \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  http://localhost:8000/v1/skills/${PX0_SKILL_ID}/versions/1/download
```

Verify your downloaded package:
```bash
unzip -l downloaded-skill.zip
```

---

## Summary of REST Endpoints

| HTTP Method | Route | Description | Role Requirement |
| --- | --- | --- | --- |
| `POST` | `/v1/projects/:projectID/skills` | Create a skill (accepts JSON or ZIP form-data) | Editor / Admin |
| `GET` | `/v1/projects/:projectID/skills` | List skills in a project | Viewer / Editor / Admin |
| `GET` | `/v1/skills/:id` | Get skill metadata (ID or slug) | Viewer / Editor / Admin |
| `PUT` | `/v1/skills/:id` | Update skill metadata (ID or slug) | Editor / Admin |
| `DELETE`| `/v1/skills/:id` | Delete skill and all versions/files | Editor / Admin |
| `GET` | `/v1/skills/:id/versions` | List versions for a skill | Viewer / Editor / Admin |
| `POST` | `/v1/skills/:id/versions` | Create an empty draft version | Editor / Admin |
| `GET` | `/v1/skills/:id/versions/:version` | Get version details | Viewer / Editor / Admin |
| `DELETE`| `/v1/skills/:id/versions/:version` | Delete draft version | Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/promote`| Promote version (`draft` $\rightarrow$ `stable` $\rightarrow$ `live`) | Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/demote` | Demote version (`live` $\rightarrow$ `stable`) | Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/archive`| Archive version | Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/duplicate`| Branch/duplicate version files to new draft | Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/upload` | Upload ZIP to replace all draft files | Editor / Admin |
| `GET` | `/v1/skills/:id/versions/:version/download` | Download version files as ZIP | Viewer / Editor / Admin |
| `GET` | `/v1/skills/:id/versions/:version/files` | List file structures (no content payload) | Viewer / Editor / Admin |
| `GET` | `/v1/skills/:id/versions/:version/files/content`| Retrieve individual file content | Viewer / Editor / Admin |
| `POST` | `/v1/skills/:id/versions/:version/files` | Create or update a file in draft | Editor / Admin |
| `PUT` | `/v1/skills/:id/versions/:version/files` | Update an existing file in draft | Editor / Admin |
| `DELETE`| `/v1/skills/:id/versions/:version/files` | Delete an individual file from draft | Editor / Admin |
