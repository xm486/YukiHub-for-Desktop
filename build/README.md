# Build Directory

This directory contains the platform metadata, packaging templates, and generated release artifacts used by the Wails v3 build scripts.

YukiHub Desktop ships on Windows and Linux amd64, so both platform asset sets are kept:

- `windows/` contains the Windows manifest, version metadata, icon, and NSIS templates.
- `linux/` contains the nfpm (deb/rpm) templates and packaging scripts.
- `bin/` is the ignored output directory for release artifacts.

Refresh the standard platform assets from `build/` with:

```shell
wails3 update build-assets -name YukiHub -binaryname YukiHub -config config.yml -dir .
```

Review generated changes after refreshing because `windows/nsis/` is part of the checked-in Wails v3 packaging setup.

## Versioned Build Assets

Use the repository script to update `info.version` in `config.yml` and refresh the Wails platform metadata:

```text
scripts\update-build-assets.bat 0.1.0
```

It accepts an optional leading `v`, requires an `X.Y.Z` version, and restores `build/config.yml` afterwards.

## Packaging

From the repository root:

```text
scripts\build.bat all <version> <amd64|arm64>
```

This produces an NSIS installer and a portable ZIP for the requested architecture.
