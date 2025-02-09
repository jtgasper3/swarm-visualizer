

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
