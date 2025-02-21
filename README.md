
Swarm Visualizer
================

The Swarm Visualizer extends on the [`dockersamples/docker-swarm-visualizer`](https://github.com/dockersamples/docker-swarm-visualizer) demo'd at the 2015 DockerCon EU keynote.

It's goals are:
 - minimize internal data being exposed to the browser
 - allow for more control of the data being viewed
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

OIDC Environment Variables:

- `ENABLE_AUTH`: `true` enables OIDC support (default: `false`)
- `OIDC_CLIENT_ID`: standard OAuth client id
- `OIDC_CLIENT_SECRET`: standard OAuth client secret
- `OIDC_REDIRECT_URL`: this app's callback url; should end in `/callback` and will be registered in the identity provider. For example, `https://myswarm.example.internal/visualizer/callback`
- `OIDC_SCOPES`: comma separated list of scopes. For example, `openid,profile,email`
- `OIDC_WELL_KNOWN_URL`: Location to lookup the identity provider's public signing key, token and authorization endpoints. For example, `https://auth.example.com/.well-known/openid-configuration`

Other Environment Variables:

- `DOCKER_API_VERSION`: adjust the Docker api version if the server needs it. (default: `1.47`)


## Development/Testing

### Turn on Swarm Mode

If not already enabled:

```sh
docker swarm init
```

### Spin up some services

```
docker service create --name nginx --replicas=3 nginx:latest
docker service create --name redis redis:latest
docker service create --name redis2 redis:latest
docker service create --name redis3 redis:latest
docker service create --name redis4 redis:latest
docker service create --name redis5 redis:latest
```

### Build and test
```sh
docker compose -f deployment/docker-compose.yml up --build
```

> The compose file mounts the static assets so they can be modified on the fly.

### Cleaning up

Stop dummy services:

```sh
 docker service rm nginx redis redis2 redis3 redis4 redis5
 ```

Remove Swarm mode:

```sh
docker swarm leave --force
```
