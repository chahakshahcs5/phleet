# CI/CD

phleet is triggered over the same authenticated HTTP API as the panel. Any CI
system that can make an HTTPS request can deploy: GitHub Actions, GitLab CI,
Jenkins, Buildkite, Forgejo Actions, a shell script, or a webhook relay.

## Create a deploy token

In the panel open **Settings → API tokens** and create a token with the
`deployer` role. It can:

- trigger `deploy`, `restart`, `start`, `stop` and `rollback`;
- read project lists, project views and deployment history.

It cannot read environment variables, repository credentials, source archives,
backups, files, users, API tokens or project configuration.

Keep the token in the CI system's secret store. Do not use the admin token.

## Trigger a deployment

```bash
curl -fsS -X POST \
  "$PHLEET_URL/api/projects/$PHLEET_PROJECT_ID/deploy" \
  -H "Authorization: Bearer $PHLEET_TOKEN"
```

The response is `202` with one deployment per eligible replica. Deployment
progress is asynchronous. Poll:

```bash
curl -fsS \
  -H "Authorization: Bearer $PHLEET_TOKEN" \
  "$PHLEET_URL/api/projects/$PHLEET_PROJECT_ID/deployments"
```

The latest deployment includes `status` (`queued`, `building`, `starting`,
`health_check`, `active`, `rolled_back`, `failed`), `startedAt`, `finishedAt`,
`release` and `commitSha` for git sources.

## Deploy a commit, tag or branch

Send a JSON body with `ref`:

```bash
curl -fsS -X POST \
  "$PHLEET_URL/api/projects/$PHLEET_PROJECT_ID/deploy" \
  -H "Authorization: Bearer $PHLEET_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"ref":"v1.2.3"}'
```

`ref` may be a branch, tag or commit SHA. The phone clones it shallowly and, for
a commit that is not the branch tip, fetches that one commit. The deployment
records the checkout SHA. A stack deploys every service at the same ref.

Other lifecycle actions:

```bash
POST /api/projects/{id}/restart
POST /api/projects/{id}/stop
POST /api/projects/{id}/start
POST /api/projects/{id}/rollback        # previous release, all replicas
POST /api/projects/{id}/rollback        # {"serverId":"...","release":"..."}
```

## Git webhooks

Generate a secret under **Project → Settings → Webhooks** (or the stack’s
webhook card). phleet shows:

- GitHub: `POST /hooks/github/<project-id>` with `X-Hub-Signature-256`.
- GitLab: `POST /hooks/gitlab/<project-id>` with `X-Gitlab-Token`.

A push to the configured branch deploys the pushed commit. Other branches and
non-push events are ignored with `202`. Rotate the secret from the same card;
the old one stops working immediately.

## GitHub Actions

This repository includes a ready-to-copy example at
[`examples/github-actions/deploy.yml`](../examples/github-actions/deploy.yml).
Copy it into the repository of the project you are deploying, not into the
phleet control-plane repository. It runs on manual dispatch, and on pushes to
`main` once `PHLEET_PROJECT_ID` is configured as a repository variable.

Store `PHLEET_URL` and `PHLEET_TOKEN` as repository secrets, and
`PHLEET_PROJECT_ID` as a repository variable. The manual dispatch form also
accepts a project ID.

The equivalent inline workflow is:

```yaml
name: Deploy

on:
  push:
    branches: [main]
  workflow_dispatch:

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Trigger phleet deploy
        env:
          PHLEET_URL: ${{ secrets.PHLEET_URL }}
          PHLEET_PROJECT_ID: ${{ secrets.PHLEET_PROJECT_ID }}
          PHLEET_TOKEN: ${{ secrets.PHLEET_TOKEN }}
        run: |
          curl -fsS -X POST \
            "$PHLEET_URL/api/projects/$PHLEET_PROJECT_ID/deploy" \
            -H "Authorization: Bearer $PHLEET_TOKEN"
```

To wait for the result, poll the deployments endpoint until the newest
deployment reaches `active`, `rolled_back` or `failed`.

## GitLab CI

```yaml
deploy:
  stage: deploy
  only:
    - main
  script:
    - >
      curl -fsS -X POST
      "$PHLEET_URL/api/projects/$PHLEET_PROJECT_ID/deploy"
      -H "Authorization: Bearer $PHLEET_TOKEN"
```

## Generic webhook relay

If a provider sends a webhook, translate it in a tiny function/worker that
calls the deploy endpoint. phleet does not currently verify GitHub/GitLab
webhook signatures itself; the endpoint requires the deploy token instead.

## Branches

A project has one configured `branch` (default `main`). Deploys run a shallow
clone of that branch, so each deployment uses the latest commit on the branch
at deploy time. The resulting commit SHA is stored in deployment history.

There are no automatic preview environments per branch. To deploy multiple
branches at once:

- create one project per branch/environment (for example `api-main` on `main`
  and `api-develop` on `develop`), each with its own subdomain and env; or
- change the single project's branch, accepting that the same hostname now
  serves that branch and the last deploy wins.

Stacks share the parent branch across all services.

## Cloudflare Access

If Cloudflare Access protects `fleet.example.com`, CI requests need an Access
service token and the
`CF-Access-Client-Id` / `CF-Access-Client-Secret` headers, or a bypass policy
for the deployment API path. Keep `/agent`, `/agent/data` and `/hooks/*` on
their own bypass: Git providers cannot carry an Access session, and the webhook
signature authenticates those paths.

## Notes

- Deploys are built and run entirely on the phone; the CI runner only triggers
  and observes.
- The deploy endpoint accepts the current project configuration and an optional
  `ref`. Without a ref it deploys the configured branch at trigger time.
- Build logs are available at `GET /api/deployments/{id}` to admin tokens.
