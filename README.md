
Swarm Visualizer
================
[Source Code](https://github.com/jtgasper3/swarm-visualizer) | [Docker Image](https://hub.docker.com/r/jtgasper3/swarm-visualizer)

The Swarm Visualizer extends on the [`dockersamples/docker-swarm-visualizer`](https://github.com/dockersamples/docker-swarm-visualizer) concept demo'd at the 2015 DockerCon EU keynote.

Its goals are:
 - minimize and sanitize internal Swarm data being exposed to the browser
 - allow for more control of the data being displayed
 - implement authentication via OpenID Connect

## Usage

### Docker CLI

```sh
docker service create visualizer \
    -e CLUSTER_NAME="Dev Cluster" \
    -p 8080:8080 \
    -v /var/run/docker.sock:/var/run/docker.sock \
    --constraint node.role==manager
    jtgasper3/swarm-visualizer
```

### Docker Compose

```yaml
service:
  viz:
    image: jtgasper3/swarm-visualizer:latest
    deploy:
      placement:
        constraints:
          - node.role==manager
    environment:
      CLUSTER_NAME: Dev Cluster
    ports:
      - 8080:8080
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
```

## Configuration

General Environment Variables:

- `CLUSTER_NAME`: title to display on the main page
- `CONTEXT_ROOT`: the context root of the web app; useful when working with reverse-proxies (default: `/`)
- `LISTENER_PORT`: port to listen on (default: `8080`)
- `HIDE_ALL_CONFIGS`: hides all configs values (default: `false`)
- `HIDE_ALL_ENVS`: hides all environment variables values (default: `false`)
- `HIDE_ALL_MOUNTS`: hides all mounts values (default: `false`)
- `HIDE_ALL_SECRETS`: hides all secrets values (default: `false`)
- `HIDE_LABELS`: comma list of values that hides labels values from `all`, `container`, `network`, `node`, `service` (default: `(nothing)`)
- `SENSITIVE_DATA_PATHS`: comma delimited path of values to remove from the exported data. See *Data Sanitization* below

OIDC Environment Variables:

- `ENABLE_AUTHN`: `true` enable OIDC authentication support (default: `false`)
- `OIDC_CLIENT_ID`: standard OAuth client id
- `OIDC_CLIENT_SECRET_FILE`: path to file containing a standard OAuth client secret
- `OIDC_REDIRECT_URL`: this app's callback url; should end in `/callback` and will be registered in the identity provider. For example, `https://myswarm.example.internal/visualizer/callback`
- `OIDC_SCOPES`: comma separated list of scopes. For example, `openid,profile,email`
- `OIDC_WELL_KNOWN_URL`: location to look up the identity provider's public signing key, token and authorization endpoints. For example, `https://auth.example.com/.well-known/openid-configuration`
- `OIDC_AUTH_URL`: authorization endpoint URL; overrides the value from `OIDC_WELL_KNOWN_URL` if set
- `OIDC_TOKEN_URL`: token endpoint URL; overrides the value from `OIDC_WELL_KNOWN_URL` if set
- `OIDC_USERNAME_CLAIM`: JWT claim to use as the display username (default: `preferred_username`)
- `OIDC_SESSION_MAX_AGE`: lifetime of the session cookie in seconds (default: `3600`)

Other Environment Variables:

- `DOCKER_API_VERSION`: adjust the Docker api version if the server needs it. (default: `(negotiated)`)
- `TRUSTED_PROXIES`: comma-separated list of trusted reverse-proxy IP addresses or CIDR ranges. When set, the `X-Real-IP` and `X-Forwarded-For` headers are trusted for rate limiting purposes when the direct connection originates from a listed address. Plain IPs are accepted alongside CIDR notation (e.g. `10.0.0.0/8,192.168.1.5`). **Only set this if the application port is not directly reachable by untrusted clients**, otherwise clients can spoof their IP to bypass rate limits.

### Reverse Proxy Considerations

When running behind a reverse proxy (such as Traefik or nginx), set `TRUSTED_PROXIES` to the proxy's IP or subnet and `CONTEXT_ROOT` to the path prefix if the app is not served from `/`. For example:

```yaml
environment:
  CONTEXT_ROOT: /visualizer/
  TRUSTED_PROXIES: 10.0.0.0/8
```

## Data Sanitization

The Docker API can expose potentially sensitive information. There are several methods to sanitize data from the payload that can be tailored to your needs:

- Using the environment variables of `HIDE_ALL_CONFIGS`, `HIDE_ALL_ENVS`, `HIDE_ALL_MOUNTS`, and `HIDE_ALL_SECRETS` with the value of `true` will cause the application to strip the respective values from the output sent to the browser.
- The environment variable `HIDE_LABELS` can be used to strip the output of various labels using a comma separated list of `container`, `network`, `node`, `service`. The value of `all` can also be used instead of specified all of the values.
- To manage things on a service by service level, use labels on the desired service (with `io.github.jtgasper3.visualizer.hide-labels`) and environment variables (`io.github.jtgasper3.visualizer.hide-envs`) to specify a comma separated list of label or environment variables to remove from the service's specific labels or environment variable values from the output. The value is changes to "(sanitized)".

For very granular control over uses that we didn't consider, use the environment variable of `SENSITIVE_DATA_PATHS` and a comma separated list of paths to remove. Examine the JSON output and find and specify the path to remove. Use `*` for arrays, and use single quotes to delimit values of property names that have embedded periods (i.e. `services.*.Spec.TaskTemplate.ContainerSpec.Labels.'desktop.docker.io/mounts/0/Source'`).


## Development/Testing

### Turn on Swarm Mode

If not already enabled:

```sh
docker swarm init
```

### Build and test
```sh
docker compose -f deployment/docker-compose.yml up --watch
```

> The compose file mounts the static assets so they can be modified on the fly.


### Spin up some test stacks and individual services

```
docker stack deploy -c test/docker-stack-test.yml test_1
docker stack deploy -c test/docker-stack-test.yml test_2
docker service create --name httpd httpd:2.4
```

### Cleaning up

Stop dummy services:

```sh
docker service rm httpd
docker stack rm test_1
docker stack rm test_2
```

Remove Swarm mode:

```sh
docker swarm leave --force
```
