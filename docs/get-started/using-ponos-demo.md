---
description: A working demo setup using the public Playground repo.
---

# Using Ponos (demo)

This demo uses the public Playground repo so you can test upgrades and
diagnostics without wiring a private repository.

Repository:
https://github.com/blockopsnetwork/playground

Example manifest used in this demo:
https://github.com/blockopsnetwork/playground/blob/main/ethereum/ethereum-hoodi.yaml

## Example config

Add this to your `ponos.yml` under `projects`:

```yaml
projects:
  - network: "ethereum"
    project_name: "hoodi"
    owner: "blockopsnetwork"
    name: "playground"
    branch: "main"
    paths:
      - "ethereum/ethereum-hoodi.yaml"
```

## Demo video

<figure><img src="../.gitbook/assets/ponos-demo-placeholder.png" alt=""><figcaption>Upgrade + diagnostics walkthrough</figcaption></figure>

Replace the placeholder with your recorded demo.
