# 02주차 과제

## 학습 내용
* Kubernetes Pod를 왜 컨테이너 집합이라고 표현하는지
* Pod 생성 요청이 발생하면 Kublet -> CRI -> OCI가 어떻게 동작하는지?
* CRI에서 Pod에서 컨테이너간 공유해야할 Namespace 컨텍스트를 어떻게 관리하는지?
* OCI는 RuntimeSpec 중 Namespace spec에서 path의 존재 유무에 따른 동작 차이
* Pod가 추가되면 CNI는 무슨 동작을 하는지?

## 목표
1주차 과제에서 구현한 network namespace 분리 기능을 확장한다.

이번 과제에서는 named network namespace를 생성하고, netlink socket을 이용해 veth pair를 생성한 뒤, 한쪽 veth는 host network namespace에 두고 다른 한쪽 veth는 named network namespace안으로 이동시킨다.

이후 각 veth에 IP Address를 설정하고, named network namespace 안에서 서버 프로세스를 실행한다.

최종적으로 host network namespace에서 named network namespace 내부의 서버 프로세스로 요청을 보냈을 때 정상 응답을 받을 수 있어야 한다.

이번 과제는 다음 내용을 검증한다.
* named network namespace 생성 여부
* veth pair 생성 여부
* veth peer의 namespace 이동 여부
* host veth와 namespace veth의 IP 설정 여부
* namespace 내부 process 실행 여부
* host namespace에서 named network namespace 내부 서버로 통신 가능 여부

다음은 구현하지 않는다.
* route 설정
* bridge 연결
* NAT 설정

## 구현 요구사항
1. 서버는 8080 포트로 Listen 한다.
2. named network namespace 생성
   1. 서버는 `/netns` URL path를 제공해야 한다.
   2. `/netns`는 POST 요청을 받는다.
   3. `/netns`는 요청을 받으면 새로운 network namespace를 생성해야 한다.
   4. Request Body는 JSON 형식이며 다음 필드를 가진다
		```json
		{
			"name": "test-01"
		}
		```
	* name은 생성할 named network namespace의 이름이다.
   5. name은 `/var/run/netns/{name}` 경로의 파일 이름으로 사용된다.
   6. network namespace 생성은 부모 HTTP API 서버의 network namespace에 영향을 주면 안된다.
   7. 부모 HTTP API 서버는 계속 host network namespace 안에서 실행되어야 한다.
   8. API Response는 JSON 형식으로 다음 값을 반환해야 한다.
		```json
		{
			"name": "test-01",
			"netns_path": "/var/run/netns/test-01"
		}
		```
   9. netns_path는 생성된 named network namespace를 가리키는 경로여야 한다.
3. veth pair 생성 및 설정
   1. 서버는 /netns/{name}/veth URL path를 제공해야 한다
   2. /netns/{name}/veth는 POST 요청을 받는다.
   3. Request body는 JSON 형식이며 다음 필드를 가진다.
		```json
		{
			"host_ifname": "veth-test01",
			"peer_ifname": "eth0",
			"host_ip": "10.10.0.1/24",
			"peer_ip": "10.10.0.2/24"
		}
		```
	* host_ifname은 host network namespace에 생성될 veth interface 이름이다.
	* peer_ifname은 named network namespace 내부에서 사용할 veth interface 이름이다.
	* host_ip는 host veth에 설정할 IP address와 prefix length이다.
	* peer_ip는 namespace 내부 veth에 설정할 IP address와 prefix length이다.
   4. /netns/{name}/veth는 요청을 받으면 veth pair를 생성해야 한다.
   5. ip link add, ip addr add, ip link set, ip netns exec 같은 외부 명령어를 프로그램 내부에서 호출 하면 안된다.
   6. 생성된 veth pair 중 한쪽은 host network namespace에 남아 있어야 한다.
   7. 생성된 veth pair 중 다른 한쪽은 /var/run/netns/{name}이 가리키는 network namespace 안으로 이동해야 한다.
   8. host network namespace에 남은 veth interface 이름은 request body의 host_ifname 값이어야 한다.
   9. named network namespace 안으로 이동한 veth interface 이름은 request body의 peer_ifname 값이어야 한다.
   10. host veth interface에는 request body의 host_ip 값이 설정되어야 한다.
   11. peer veth interface에는 request body의 peer_ip 값이 설정되어야 한다.
   12. host veth interface는 UP 상태여야 한다.
   13. peer veth interface는 UP 상태여야 한다.
   14. named namespace 내부 loopback interface인 lo도 UP 상태여야 한다.
4. named network namespace 안에서 process 실행
   1. 서버는 /netns/{name}/exec URL path를 제공해야 한다.
   2. /netns/{name}/exec는 POST 요청을 받는다.
   3. Request body는 JSON 형식이며 다음 필드를 가진다
		```json
		{
			"path": "/usr/bin/echoserver",
			"args": [""]
		}
		```
   4. /netns/{name}/exec는 요청을 받으면 path, args를 이용해 프로그램을 실행한다.
   5. 실행된 프로그램은 /var/run/netns/{name}이 가리키는 network namespace 안에서 실행되어야 한다.
   6. API Response는 JSON 형식으로 다음 값을 반환해야 한다.
		```json
		{
			"name": "test-01",
			"parent_pid": 12345,
			"child_pid": 12346
		}
		```