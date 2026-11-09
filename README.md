# slop-strata

Analyze code changes for quality over time (eg presence of slop)

## Installation



### GH CLI Extension

> **Note:** Requires a [GitHub Release](https://github.com/kyleking/slop-strata/releases) with precompiled binaries. See [CONTRIBUTING.md](CONTRIBUTING.md#releases) for creating the first release.

```bash
gh extension install kyleking/slop-strata
```

### Homebrew

goreleaser publishes a cask to [`kyleking/homebrew-tap`](https://github.com/kyleking/homebrew-tap) with each release, so the install needs `--cask`:

```bash
brew install --cask kyleking/tap/slop-strata
```

### Go Install


```bash
go install github.com/kyleking/slop-strata/cmd/slop-strata@latest
```


## Development

```bash
mise install && hk install --mise
mise run ci
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for full development workflow.
