# sonic-lab
[![Test Docker Compose](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml/badge.svg)](https://github.com/mestery/sonic-lab/actions/workflows/test-docker-compose.yml)

This is a test repository for sonic lab which uses docker-sonic-vs container
images. The lab will build the following topology:

```
        +--------+
        | Spine  |
        +--------+
        ||      ||
   link1||      ||link2
        ||      ||
+--------+      +--------+
| Leaf1  |      | Leaf2  |
+--------+      +--------+
```

## Start Colima

Start colima with amd64 support using rosetta:

```
colima start --vm-type vz --vz-rosetta --cpu 4 --memory 8 --mount-type virtiofs
```

## Execute build script

Confirm it is executable:

```
chmod +X start.sh
```

If you need the docker-sonic-vs image:

```
./start.sh -l
```

If it's already loaded:

```
./start.sh
```

## Login to the containers

To login to the containers and run commands on the spine:

```
docker exec -it sonic-spine bash
```

And on the leaf containers:

```
docker exec -it sonic-leaf1 bash
docker exec -it sonic-leaf2 bash
```

## Cleanup the lab

```
docker compose down -docker-sonic-vs
```