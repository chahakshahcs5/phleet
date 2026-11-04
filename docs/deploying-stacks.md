# Deploying a multi-service stack

A **stack** is one phleet project with several child services. It is the right shape for a
repository that contains more than one long-running process — for example a Next.js portal, an
API and an MCP server — because the UI still treats them as one project while each service gets
its own subdomain, port, process, health check, releases and logs.

This page is a worked example for a pnpm monorepo. The same rules apply to npm, Yarn or a plain
Node project.

## 1. Shape of the deployment

```
app-stack            parent: repo/bundle, branch, env, addons
├── web    <slug>.<domain>   :4103  /health  -> proxies to the API
├── api    <slug>-api.<domain> :4104  /health
└── mcp    <slug>-mcp.<domain> :4105  /health
```

- The parent owns the source (a Git repository or an uploaded folder) and the encrypted
  environment.
- Each child has its own build command, start command and health path.
- A child deploy reads the **parent's** source bundle, so an individual service can be
  redeployed without re-uploading the repository.
- Pin the stack to one phone so the children can reach each other over `127.0.0.1`.

## 2. Make the app phone-friendly first

1. **Honour `$PORT`.** phleet assigns a port per service and injects `PORT`; the process must
   listen on it and on loopback (phleet also sets `HOST=127.0.0.1`). The agent proxies to
   `127.0.0.1`, so binding `0.0.0.0` is unnecessary and exposes the port on the LAN.
2. **Expose a health endpoint.** `/health` should return `200` once the process can serve
   traffic. phleet accepts `200–499`; a connection refusal or a timeout fails the deploy and
   rolls back.
3. **Do not hard-code internal URLs.** Read the API address from an environment variable
   (`API_INTERNAL_URL`, `NEXT_PUBLIC_API_URL`, …) and set it per service.
4. **Keep the database schema out of the phone build.** Prisma's schema engine and most native
   migration tools do not run on Termux. Push the schema from your PC through the addon's
   **PC bridge**, and commit the generated client so the phone only consumes it.

## 3. Add the stack

**New project → Multi-service stack**, then:

| Field | Web | API | MCP |
|---|---|---|---|
| Name | `Web` | `API` | `MCP` |
| Subdomain | `app` | `app-api` | `app-mcp` |
| Build command | `pnpm install --frozen-lockfile && pnpm --filter web build:webpack` | `pnpm install --frozen-lockfile` | `pnpm install --frozen-lockfile` |
| Start command | `pnpm --filter web start` | `pnpm --filter api start` | `pnpm --filter mcp start` |
| Health path | `/health` | `/health` | `/health` |

Each service also has a **kind**: `web app` (the default), `static site`, or `worker`. A static
service serves a directory from its release (set the static directory, e.g. `dist`) and needs no
start command. A worker runs a supervised command with no port or hostname, which suits queue
consumers and background jobs in the same repository. A stack can mix all three.

Per-service environment overrides the stack environment at deploy time. A typical web service
needs the private URL of the API on the same phone:

```
API_INTERNAL_URL=http://127.0.0.1:<api-port>
NEXT_PUBLIC_API_URL=https://app-api.example.com
```

Each service has its own `PHLEET_DATA_DIR` (`~/apps/<service-slug>/shared/data`). Use it for
uploads, local SQLite files and caches that must survive the next deploy; the release directory
itself is replaced on every release.

`NEXT_PUBLIC_*` values are inlined at build time, so set the public URL before the first build.

## 4. Build commands that survive Termux

### Next.js 16 needs webpack, not Turbopack

Next 16 defaults to Turbopack. Turbopack requires native bindings, and there are none for
`android/arm64`; the build stops with:

```
Turbopack is not supported on this platform (android/arm64) because native bindings are not
available. Only WebAssembly (WASM) bindings were loaded.
```

Add a script and use it on the phone:

```json
{
  "scripts": {
    "build": "next build",
    "build:webpack": "next build --webpack"
  }
}
```

phleet installs `@next/swc-wasm-nodejs` as an optional dependency, so the webpack build works
without a native SWC binary. Expect a few minutes for the first build on a phone.

### Keep the install reproducible

`pnpm install --frozen-lockfile` (or `npm ci`) makes the phone build match the lockfile you
tested on the PC. Do not copy `node_modules` between machines; native packages such as `esbuild`
must resolve to the phone's own binary.

### Watch the deploy timeout

A deploy has **15 minutes** including install, build and the health check. A cold Next.js build
on an older phone can take most of that. If it times out, build only the app you are deploying
(`pnpm --filter web build:webpack`) instead of the whole workspace, and keep the lockfile warm
on the phone.

## 5. Deploy in dependency order

Deploy the API (and any other backing service) **before** the web. A common pattern is for the
web's `/health` to proxy to the API:

```js
// next.config.mjs
async rewrites() {
  const api = process.env.API_INTERNAL_URL || 'http://127.0.0.1:4000';
  return [{ source: '/health', destination: `${api}/health` }];
}
```

If the API is down, the web's health check fails and the web deploy rolls back even though the
web itself started correctly.

The deploy queue is serialized per agent, so start the API first and wait for it to become
active before starting the web.

## 6. Data services

Create PostgreSQL/Redis from the addon wizard and **bind the addon to the stack parent**. phleet
injects `DATABASE_URL` and `REDIS_URL` into every child on that phone; do not set them manually.

```
Addons → app-db → Bind to project → app-stack
```

The addon listens on `127.0.0.1` on the phone. To apply a schema from your PC, use the bridge
port shown on the addon page:

```powershell
$env:DATABASE_URL = 'postgresql://user:pass@127.0.0.1:15400/db?schema=public'
pnpm db:setup --accept-data-loss
```

The bridge is loopback-only by default; `-bridge-lan` is a deliberate opt-in.

## 7. Uploaded source vs Git

Both work for a stack. With an **uploaded folder**:

- the zip lives on the control plane under the **parent** id;
- every child downloads and extracts the same bundle;
- replacing the source is done on the parent, then each child can be redeployed.

The bundle is capped at 64 MB and is not encrypted at rest (unlike project environment
variables). Keep build output (`.next`, `dist`, `node_modules`) out of the zip.

## 8. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `Turbopack is not supported on this platform` | Build with `next build --webpack`; see section 4. |
| Web deploy fails its health check while the app is fine | The web's `/health` proxies to the API. Deploy the API first, or point the health path at a route the web serves itself. |
| `upload a source folder first` on a child deploy | The source belongs to the stack parent. Upload it there; child deploys read the parent bundle. |
| API exits on startup with a production env error | Production builds validate secrets (JWT, OAuth clients, S3). Add them to the **stack** environment so every child inherits them, then redeploy. |
| Build runs out of time | Narrow the build to one workspace package and re-run; the 15-minute limit covers install + build + health check. |
| `DATABASE_URL is not set` | Bind the addon to the stack parent, not to an individual child, and redeploy. |
