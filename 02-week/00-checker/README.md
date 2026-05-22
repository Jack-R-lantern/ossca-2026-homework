# 02-week 00-checker 사용법

`00-checker`는 02주차 과제 구현 결과를 단계별로 확인하는 도구이다.

검사는 다음 세 단계로 나뉜다.

1. named network namespace가 `nsfs`로 mount 되었는지 확인
2. host와 named namespace 사이의 veth pair, IP, link 상태를 확인
3. named namespace 안에서 실행된 echo server에 host namespace에서 접속 가능한지 확인

## 빌드

`00-checker`는 여러 Go 파일로 구성된 하나의 `main` package이므로 디렉터리 전체를 빌드해야 한다.

```bash
cd 02-week/00-checker
make build
```

## 테스트 준비

과제 서버와 echo server를 먼저 빌드하고, 과제 서버를 root 권한으로 실행한다. network namespace, veth, interface IP 설정은 root 권한이 필요하다.

```bash
cd 02-week/01-echoserver
make build

cd ../..
# 과제 구현 서버 위치에서 실행한다. 예시는 서버 바이너리 이름이 server인 경우이다.
sudo ./server
```

아래 예시는 다음 값을 사용한다.

```bash
NAME=test-01
HOST_IF=veth-test01
PEER_IF=eth0
HOST_IP=10.10.0.1/24
PEER_IP=10.10.0.2/24
PEER_ADDR=10.10.0.2
```

## 1. named network namespace 검사

먼저 과제 서버의 `/netns` API로 named network namespace를 만든다.

```bash
curl -X POST http://127.0.0.1:8080/netns \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-01"}'
```

응답의 `netns_path`가 `/var/run/netns/test-01` 또는 `/run/netns/test-01`을 가리키면 checker로 확인한다.

```bash
cd 02-week/00-checker
sudo ./checker netns --name test-01
```

성공하면 다음과 비슷하게 출력된다.

```text
namespace mount verified: /run/netns/test-01 uses nsfs
```

이 검사는 `/proc/self/mountinfo`를 읽어서 해당 path가 mount 되어 있고 filesystem type이 `nsfs`인지 확인한다.

## 2. veth pair 검사

과제 서버의 `/netns/{name}/veth` API로 veth pair를 생성하고 설정한다.

```bash
curl -X POST http://127.0.0.1:8080/netns/test-01/veth \
  -H 'Content-Type: application/json' \
  -d '{
    "host_ifname": "veth-test01",
    "peer_ifname": "eth0",
    "host_ip": "10.10.0.1/24",
    "peer_ip": "10.10.0.2/24"
  }'
```

checker로 veth 설정을 확인한다.

```bash
cd 02-week/00-checker
sudo ./checker veth \
  --name test-01 \
  --host-ifname veth-test01 \
  --peer-ifname eth0 \
  --host-ip 10.10.0.1/24 \
  --peer-ip 10.10.0.2/24
```

성공하면 다음과 비슷하게 출력된다.

```text
veth configuration verified: host=veth-test01(10.10.0.1/24) peer=eth0(10.10.0.2/24) namespace=/run/netns/test-01
```

이 검사는 다음 항목을 확인한다.

- named network namespace mount가 `nsfs`인지
- host 쪽 interface와 namespace 안의 interface가 모두 `veth` 타입인지
- 두 interface가 서로 peer로 연결되어 있는지
- host 쪽 veth, namespace 쪽 veth, namespace 안의 `lo`가 모두 UP 상태인지
- host 쪽 veth에 `host-ip`가 설정되어 있는지
- namespace 쪽 veth에 `peer-ip`가 설정되어 있는지

## 3. namespace 내부 서버 실행 검사

과제 서버의 `/netns/{name}/exec` API로 echo server를 named namespace 안에서 실행한다.

```bash
curl -X POST http://127.0.0.1:8080/netns/test-01/exec \
  -H 'Content-Type: application/json' \
  -d '{
    "path": "/absolute/path/to/02-week/01-echoserver/echoserver",
    "args": []
  }'
```

응답으로 받은 `child_pid`를 사용해 checker를 실행한다.

```bash
cd 02-week/00-checker
sudo ./checker server \
  --name test-01 \
  --pid <child_pid> \
  --listen-ip 10.10.0.2
```

echo server가 8080이 아닌 다른 포트로 listen 하도록 바꿨다면 `--port`를 추가한다.

```bash
sudo ./checker server \
  --name test-01 \
  --pid <child_pid> \
  --listen-ip 10.10.0.2 \
  --port 8080
```

성공하면 다음과 비슷하게 출력된다.

```text
server execution verified: pid=<child_pid> namespace=/run/netns/test-01 listen=10.10.0.2:8080
```

이 검사는 다음 항목을 확인한다.

- `child_pid`가 host network namespace가 아닌 named network namespace 안에서 실행 중인지
- `child_pid`의 network namespace가 `/run/netns/{name}`과 같은 namespace인지
- host namespace에서 `http://{listen-ip}:{port}/`로 HTTP 요청을 보낼 수 있는지
- echo server 응답의 `pid`가 `child_pid`와 같은지
- echo server 응답의 `local_addr`가 `{listen-ip}:{port}`와 같은지
- echo server 응답의 `namespace_ref`가 `child_pid`의 `/proc/{pid}/ns/net` reference와 같은지

## 전체 실행 예시

```bash
cd 02-week/00-checker
go build -o checker .

curl -X POST http://127.0.0.1:8080/netns \
  -H 'Content-Type: application/json' \
  -d '{"name":"test-01"}'
sudo ./checker netns --name test-01

curl -X POST http://127.0.0.1:8080/netns/test-01/veth \
  -H 'Content-Type: application/json' \
  -d '{"host_ifname":"veth-test01","peer_ifname":"eth0","host_ip":"10.10.0.1/24","peer_ip":"10.10.0.2/24"}'
sudo ./checker veth --name test-01 --host-ifname veth-test01 --peer-ifname eth0 --host-ip 10.10.0.1/24 --peer-ip 10.10.0.2/24

curl -X POST http://127.0.0.1:8080/netns/test-01/exec \
  -H 'Content-Type: application/json' \
  -d '{"path":"/absolute/path/to/02-week/01-echoserver/echoserver","args":[]}'
sudo ./checker server --name test-01 --pid <child_pid> --listen-ip 10.10.0.2
```

## 실패할 때 확인할 것

- checker는 실패 시 어떤 조건이 맞지 않는지 에러 메시지를 출력한다.
- veth와 server 검사는 root 권한이 필요하므로 `sudo`로 실행한다.
- `--name test-01`은 기본적으로 `/run/netns/test-01`을 검사한다. 다른 경로를 직접 검사하려면 `--path /var/run/netns/test-01`처럼 지정할 수 있다.
- server 검사는 최대 5초 동안 echo server 응답을 기다린다. timeout이 발생하면 veth IP, link UP 상태, echo server 실행 여부를 먼저 확인한다.
