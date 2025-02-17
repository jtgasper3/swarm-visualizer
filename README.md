

## Configuration

Environment Variables:
- `CLUSTER_NAME`: title to display on the main page
- `LISTENER_PORT`: port to listen on (default: `8080`)
- `ROOT_CONTEXT`: the root context of the app (default: `/`)

## Local testing

```sh
go run main.go
```

## 

```sh
docker swarm init
```

```
docker service create --name nginx --replicas=3 nginx:latest
docker service create --name redis redis:latest
```

```sh
docker-compose up --build
```
