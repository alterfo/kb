# sources.yaml format

`kb sync` reads connector instances from `$KB_ROOT/sources.yaml` (configurable
via `KB_ROOT`). The file declares which sources to sync and which environment
variables hold their secrets.

## Rules

- A source needs `name` (unique across all sources — the same name cannot be
  reused by a different `type`, because sink directories, pruning, and
  retrieval filters key on the name alone) and `type` (a registered
  connector type).
- `config` values are connector-specific plain strings.
- `secrets` maps field names to **env-var names** (uppercase `[A-Z][A-Z0-9_]*`).
  Literal secret values are rejected by the parser. Actual values are read from
  the process environment at resolve time and are never persisted.
- `virtual_collections` maps a collection name to a list of source-type globs
  (`type:*`) used as metadata filters in retrieval.

## Example

```yaml
sources:
  - name: main-org
    type: github
    config:
      org: my-org
    secrets:
      token: GITHUB_TOKEN

  - name: internal-gitlab
    type: gitlab
    config:
      base_url: https://gitlab.example.com
    secrets:
      token: GITLAB_TOKEN

  - name: engineering-wiki
    type: wiki
    config:
      variant: confluence
      base_url: https://example.atlassian.net/wiki
      space: ENG
    secrets:
      email: CONFLUENCE_EMAIL
      token: CONFLUENCE_TOKEN

  - name: support-chat
    type: telegram
    config:
      base_url: https://api.telegram.org
    secrets:
      token: TELEGRAM_BOT_TOKEN

  - name: legal-docs
    type: file
    config:
      path: ./kb_root/legal
      visibility: internal

virtual_collections:
  chats:
    - "telegram:*"
    - "slack:*"
    - "mattermost:*"
  requirements:
    - "wiki:*"
    - "tracker:*"
  code:
    - "github:*"
    - "gitlab:*"
```

## Per-connector options

### github
- `config.org` — organization (or `config.repos` — comma-separated explicit repos)
- `config.base_url` (default `https://api.github.com`), `web_base_url`, `raw_base_url`
- `config.include_wiki` (default `true`), `config.include_contents` (default `true`)
- `secrets.token` — env var name, e.g. `GITHUB_TOKEN`

### gitlab
- `config.group` — group (or `config.projects` — comma-separated explicit projects)
- `config.base_url` (default `https://gitlab.com/api/v4`), `web_base_url`
- `config.include_wiki` (default `true`), `config.include_files` (default `true`)
- `secrets.token` — env var name, e.g. `GITLAB_TOKEN`

### wiki
- `config.variant` — `mediawiki` or `confluence` (required)
- `config.base_url` — API endpoint (required); `web_base_url` derived if omitted
- `config.namespace` (MediaWiki, default `0`); `config.space` + `config.wiki` (Confluence)
- `secrets.token` (both), `secrets.email` (Confluence)

### mcp
- `config.transport` — `stdio` (default) or `http`
- stdio: `config.command` — server command (required)
- http: `config.url` — server URL (required)
- `secrets.token` — optional bearer token env-var name

### telegram
- `config.base_url` (default `https://api.telegram.org`)
- `secrets.token` — bot token env-var name

### slack
- `config.base_url` (default `https://slack.com/api`)
- `config.channels` — comma-separated channel filter (optional)
- `secrets.token` — bot token env-var name

### mattermost
- `config.base_url` (required), `config.web_base_url` (defaults to base_url)
- `config.team` — team name; `config.channels` — channel filter (optional)
- `secrets.token` — personal access token env-var name

### yandex-tracker
- `config.base_url` (default `https://api.tracker.yandex.net`), `web_base_url`
- `config.org_id` — organization id; `config.queues` — comma-separated queues
- `secrets.token` — OAuth token env-var name (e.g. `YANDEX_TRACKER_OAUTH_TOKEN`)

### youtrack
- `config.base_url` (required), `web_base_url` (defaults to base_url)
- `secrets.token` — permanent token env-var name

### kaiten
- `config.base_url` (required), `web_base_url` (defaults to base_url)
- `secrets.token` — token env-var name

### weeek
- `config.base_url` (default `https://api.weeek.net`), `web_base_url`
- `secrets.token` — token env-var name

### searchapi
Generic search-API connector; mapping between API fields and Document fields:
- `config.search_url`, `config.query`, `config.query_param` (default `q`)
- `config.items_path` — JSON path to the result array
- `config.field_id` / `field_title` / `field_url` / `field_updated_at` / `field_body`
  (defaults: `id` / `title` / `url` / `updated_at` / `body`)
- `config.kind` — document kind (default `result`); `config.visibility`
- `config.since_param` + `config.since_layout` (default RFC3339) — incremental cursor
- `config.pager` — pagination kind (`offset`|`page`|`cursor`), `pager_header`,
  `pager_param` (default `offset`), `pager_path`, `pager_count_path`, `pager_layout`,
  `pager_page_size` (default `25`), `pager_step`, `pager_until`
- `config.auth_kind` — `none` | `bearer` | `basic` | `apikey`; `config.auth_header` (default `X-Api-Key`)
- `secrets.*` — env-var names for each auth key: `secrets.token` for `bearer`/`apikey`,
  `secrets.username` + `secrets.password` for `basic` (e.g. `secrets.token: SEARCH_API_KEY`)

### file
- `config.path` — directory to import (required); `config.visibility`
- no secrets; file importers apply by extension (`.pdf`, `.xlsx`, `.json`, `.sql`)

### discord
- `config.guild_id` — guild (server) id
- `config.channels` — comma-separated channel ids (required)
- `config.base_url` (default `https://discord.com/api/v10`), `web_base_url`
  (default `https://discord.com`)
- `secrets.token` — bot token env-var name, e.g. `KB_DISCORD_TOKEN`
- optional SOCKS5 proxy via `KB_SOCKS_PROXY` (`socks5://host:port`)

### trello
- `config.board_id` — board id (required)
- `config.api_base` (default `https://api.trello.com`), `public_base`
  (default `https://trello.com`)
- public boards need no secrets; private boards use `secrets.key` + `secrets.token`
  (Trello API key and token env-var names)

### rss
- `config.feed_url` — RSS 2.0 feed URL (required)
- no secrets

### web
- `config.sitemap_url` — sitemap URL (supports `<urlset>` and `<sitemapindex>`)
  or `config.pages` — comma-separated page URLs; one of the two is required
- `config.content_selector` — HTML tag name whose subtree becomes the document
  body (default `main`; falls back to `article`, then `body`)
- no secrets

## Validation

- Duplicate `type:name` pairs are rejected.
- Secret values must be env-var names, not literals (parser error otherwise).
- `virtual_collections` entries of the form `type:name` must reference an
  existing source; `type:*` wildcards are allowed.
- `sources.yaml` is optional: `kb sync` is a no-op when it does not exist.
