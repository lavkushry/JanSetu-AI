# Project instructions

- Keep JanSetu accessible from outside the host's local network: bind the web service to `0.0.0.0:3100:3000` and the API to `0.0.0.0:8081:8080` in `compose.yaml`, and allow inbound TCP ports `3100` and `8081` in the host or cloud firewall.
