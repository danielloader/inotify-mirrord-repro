# inotify-mirrord-repro

Minimal reproduction for a mirrord bug where `filepath.EvalSymlinks` (which calls `lstat` on each path component) fails with `permission denied` when mirrord intercepts filesystem calls on Kubernetes secret volume mounts.

## What the Application Does

A Go application that:

1. Reads a JSON config file from a path set via `CONFIG_PATH` (default: `config.json`)
2. Resolves symlinks with `filepath.EvalSymlinks` (standard pattern for K8s secret volumes)
3. Watches the parent directory with fsnotify for file changes
4. Detects both direct file modifications and Kubernetes `..data` symlink swaps
5. Debounces changes (500ms) and reloads, printing the old and new content hash

## Repository Structure

```
.
├── main.go                        # Application source
├── config.json                    # Local config for testing outside K8s
├── Dockerfile                     # Multi-stage alpine build
├── go.mod
├── go.sum
├── manifests/
│   ├── deployment.yaml            # K8s Deployment with secret volume mount
│   └── secret.yaml                # K8s Secret containing config.json
├── .mirrord/
│   └── mirrord.yaml               # mirrord config targeting the deployment
└── .github/
    └── workflows/
        └── publish.yaml           # Publishes image to ghcr.io on push to main
```

## Reproducing

### Deploy to Kubernetes

```sh
kubectl apply -f manifests/
```

### Run locally with mirrord

```sh
mirrord exec -f .mirrord/mirrord.yaml -- go run .
```

### Run locally without mirrord (works fine)

```sh
go run .
```

Then edit `config.json` in another terminal to see the hot-reload in action.

### Updating the Secret (to test inotify)

```sh
kubectl patch secret inotify-repro-config -p '{"stringData":{"config.json":"{\"example\":\"new-value\"}"}}'
```

Kubernetes will perform an atomic symlink swap (`..data` -> new timestamped directory), which the fsnotify watcher detects.

## Mirrord Behaviour

Without any changes to the secret, and thus file changing, a storm of fsnotify events happen and only get slowed down by a debounce timer.

```text
DAP server listening at: 127.0.0.1:51775
Type 'dlv help' for list of commands.
2026/03/16 21:15:14 config loaded from /example/mount/path/config.json
2026/03/16 21:15:14 hash: f8f706377d0a587444dd5190847b658e0142f151f3d2afa85241c1ba2af3d7c2
2026/03/16 21:15:14 contents:
{
  "example": "2"
}
2026/03/16 21:15:15 watching for changes in /example/mount/path (file: config.json)
2026/03/16 21:15:16 change detected: file=..data op=CREATE
2026/03/16 21:15:16 change detected: file=config.json op=CREATE
2026/03/16 21:15:16 debounce timer reset
2026/03/16 21:15:16 change detected: file=..data op=CREATE
2026/03/16 21:15:16 debounce timer reset
2026/03/16 21:15:16 change detected: file=config.json op=CREATE
2026/03/16 21:15:16 debounce timer reset
2026/03/16 21:15:17 debounce fired, reloading config...
2026/03/16 21:15:17 config reloaded but hash unchanged
2026/03/16 21:15:19 change detected: file=..data op=CREATE
```
