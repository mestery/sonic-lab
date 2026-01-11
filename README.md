# sonic-lab

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

## Starting the lab

```
docker compose up -d
```

## Cleanup the lab

```
docker compose down -docker-sonic-vs
```
