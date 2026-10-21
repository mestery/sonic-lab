# sonic-vs for arm64

This directory contains a patch you need to build sonic-buildimage for arm64.
Here is how you do that. This is assumed to be done on a Mac with an M-series
chip with brew available.

> **Just need the image?** A prebuilt arm64 `docker-sonic-vs` image is
> available on GHCR, so you usually don't have to build it yourself:
>
> ```bash
> docker pull ghcr.io/mestery/docker-sonic-vs:latest
> docker tag ghcr.io/mestery/docker-sonic-vs:latest docker-sonic-vs:latest
> ```

## Prerequisites: Docker and multipass

Docker must be running on your Mac before you start, since the build process
uses docker. We recommend [colima](https://github.com/abiosoft/colima):

```bash
brew install colima docker
colima start
docker ps        # should succeed
```

In addition install and start a multipass VM to do the build. See [multipass](https://github.com/canonical/multipass) for more information.

```bash
brew install multipass
multipass launch --name sonic-builder --cpus 4 --memory 16G --disk 100G 24.04
```

## Instructions to build sonic-buildimage for arm64

Copy the patch file into the VM:

```bash
multipass transfer sonic-patches/sonic-arm64.diff sonic-builder:
```

Inside your multipass VM (`multipass shell sonic-builder`), install Docker
and add yourself to the `docker` group. The SONiC build must run as a
non-root user with access to the Docker socket (do not use `sudo make`):

```bash
sudo apt update && sudo apt install -y docker.io python3-pip
sudo usermod -aG docker $USER
exit
```

Log back in so the group membership takes effect, and confirm Docker works
without `sudo`:

```bash
multipass shell sonic-builder
docker ps
```

Then run the build:

```bash
git clone https://github.com/sonic-net/sonic-buildimage.git
cd sonic-buildimage
git apply ~/sonic-arm64.diff
pip3 install jinjanator --break-system-packages
sudo file=/usr/bin/j2 bash -c 'cat << "EOF" > "$file"
#!/usr/bin/env python3
import sys
from jinjanator.cli import main
if __name__ == "__main__":
    sys.exit(main())
EOF'
sudo chmod +x /usr/bin/j2
hash -r
make init
make configure PLATFORM=vs PLATFORM_ARCH=arm64
make SONIC_BUILD_JOBS=4 target/docker-sonic-vs.gz
ls -lrt target/docker-sonic-vs.gz
```

The build takes 90 minutes or so on an M5 Mac with these settings.

## Copy docker-sonic-vs out and load into colima

Now you need to get the built docker-sonic-vs image for arm64 out of the
multipass VM and load it into colima:

```bash
multipass transfer sonic-builder:/home/ubuntu/sonic-buildimage/target/docker-sonic-vs.gz ~/
docker load < ~/docker-sonic-vs.gz
```
