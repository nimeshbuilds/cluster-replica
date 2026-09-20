# Maintain these docs

The documentation site is built from Markdown with MkDocs Material and published by GitHub Actions to [GitHub Pages](https://nimeshbuilds.github.io/replicove/). GitHub Pages supports public repositories on GitHub Free, including Free organizations; see [GitHub's documentation](https://docs.github.com/en/pages/getting-started-with-github-pages/what-is-github-pages).

## Build locally

Use Python 3.12, Go, and make:

```bash
python3 -m venv .cache/docs-venv
.cache/docs-venv/bin/python -m pip install -r docs/requirements.txt
make docs
```

If you manage Python with uv, `uv venv --python 3.12 .cache/docs-venv` and `uv pip install --python .cache/docs-venv/bin/python -r docs/requirements.txt` provide the equivalent environment. No paid service, API key, or analytics account is needed.

```bash
make docs-serve
```

Open the URL MkDocs prints. Stop the preview with Ctrl-C. After editing source Markdown, rerun `make docs` to refresh the staged content (or restart `make docs-serve`). `make docs` builds `site/` and checks local links/anchors. Both `site/` and generated staging content stay ignored by Git.

## Sources of truth

| Content | Edit here |
| --- | --- |
| Pages/navigation | `docs/**/*.md` and `mkdocs.yml` |
| CLI quickstart | Root `QUICKSTART.md`; copied into the site during preparation |
| Security/contributing/roadmap/support | Corresponding root Markdown file |
| API reference | Go API comments/validation markers, then `make generate`; rendered from CRD schemas |
| CLI reference | Cobra command help in `cmd/replicove`; rendered from the built binary |
| Installation manifests | Chart templates, then `make manifests` |
| Branding | Existing `assets/brand` files and `docs/stylesheets/extra.css` |
| Dependency versions | `docs/requirements.txt`, reviewed together with a successful strict build |

`hack/docs.py prepare` stages only published documentation, copies brand assets and downloadable examples, rewrites repository-relative links, and builds the API/CLI references. Unpublished design proposals link back to GitHub instead of appearing as current feature documentation. `hack/docs.py check` checks the built site's local links, assets, and anchors.

Do not edit `.cache/docs-src` or the generated site. If a field or CLI flag changes, the next build updates the references from implementation. API schema descriptions cannot express every semantic restriction; keep the feature guides and examples current too.

## Publication

The documentation workflow builds and checks every pull request. Only a successful `main` build can deploy. The deploy job uses the `github-pages` environment and the minimal Pages/OIDC permissions; PR builds cannot deploy.

The site URL includes `/replicove/`. Keep `site_url`, canonical/social metadata, sitemap, and local link checks consistent with that prefix. There is no custom-domain dependency. The repository homepage points at the deployed docs.

## Content review

For each feature, explain who configures it, a valid YAML example, defaults and limits, observed status, and failure/cleanup behavior. Distinguish actual live evidence from render tests or planned adapters. Update the CLI and YAML entry points together when behavior changes. Verify navigation, search, code copy, dark mode, and narrow-screen readability when changing the theme.
