# Internal Test Management

This directory centralizes how internal tests are discovered and run.

The actual `*_test.go` files stay next to their packages. They use same-package
tests such as `package tools` and `package agent`, so moving them into this
directory would change their import path and break access to package-private
symbols.

## Run

From the repository root:

```bash
./internal/tests/run.sh
```

You can pass normal `go test` flags:

```bash
./internal/tests/run.sh -run TestDesktop -count=1
```

## Maintain

- Add a package path to `packages.txt` when a package gains tests.
- Remove a package path from `packages.txt` when its last test is deleted.
- Keep white-box Go tests beside the package under test.
- Only move a test file here after converting it to an external or integration
  test that imports production packages through exported APIs.
