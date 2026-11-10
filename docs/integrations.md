# Integrations

Git Messages has no external runtime integrations, webhooks, federation, authentication provider, or user-submitted data flow.

## HTTP clients

Any HTTP client can use the public REST API. The companion CLI uses the configured server and sends a project-specific User-Agent. OpenAPI JSON is available at `/api/v1/server/swagger`; GraphQL is available at `/graphql`.

## Git command pipelines

The CLI prints a single commit example from `random`, suitable for shell pipelines:

```sh
git commit -m "$(gitmessages-cli random)"
```
