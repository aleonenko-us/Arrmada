# Developing Arrmada

The backend is Go 1.26 with SQLite; the web UI is React, TypeScript and Tailwind (Vite),
embedded into the Go binary.

```sh
go run ./cmd/arrmada                  # backend (Go 1.26+) on :7878
cd web && npm install && npm run dev  # UI (Node 24) on :5173, proxies /api
```

For a single binary with the UI inside it:

```sh
cd web && npm run build && cd .. && go build -o arrmada ./cmd/arrmada
```

To build the Docker image from your checkout, put `ARRMADA_APP_IMAGE=arrmada:dev` in `.env`
(otherwise the build is tagged with the published image's name) and run
`docker compose up -d --build`. Without `--build`, `docker compose up -d` runs the published
image. A cold image build needs about 2 GB of free memory (4 GB is comfortable): the heavy
compiles take turns and size their parallel jobs to free memory (`docker/build-heavy`; set the
build argument `ARRMADA_BUILD_JOBS` to fix the count). A cold build takes roughly 20 minutes on
four cores; rebuilds after a Go or web change take under a minute.

## Checks

CI runs these on every push:

```sh
gofmt -l $(git ls-files '*.go')        # must print nothing
go vet ./... && go test -race -timeout 40m ./...
shellcheck -s sh update.sh install.sh docker/entrypoint.sh
cd web && npm run lint && npm test && npx tsc --noEmit -p . && npm run build && npm run e2e
```

`npm run e2e` runs the Playwright tests against the built UI with every API call answered
from fixtures in `web/e2e`, so no backend is needed.

## Screenshots

The pictures in the README and on the website (`docs/images/*.png`) are taken from those same
fixtures — never a real server:

```sh
cd web && npm run build
README_SHOTS=1 E2E_NO_BUILD=1 npx playwright test --project=readme-shots --project=readme-shots-phone
```

`web/e2e/readme-shots.spec.ts` lists the pages; posters are made-up gradient landscapes.

## Testing the installer

`install.sh` and `update.sh` take `ARRMADA_REPO_URL`, `ARRMADA_BRANCH`, `ARRMADA_API_URL` and
`ARRMADA_TARBALL_BASE` so a fork (or a test) can point them at its own copy, and `--image` to
run any image. Test them in a throwaway `docker:dind` container, never on a real server.
