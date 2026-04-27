# Implementation Log

이 문서는 OSProfiler Redis trace를 OTLP로 변환해 Tempo에 보내는 bridge를 구현하면서 겪은 주요 문제와 판단을 정리한 일지다.

목표는 처음부터 OpenStack 전체 observability 플랫폼을 만드는 것이 아니라, 다음 흐름을 안정적으로 성립시키는 것이었다.

```text
OSProfiler -> Redis -> Python helper -> Go OTLP exporter -> OTel Collector -> Tempo
```

## 1. Redis raw 데이터를 Go에서 직접 파싱할지 여부

### 왜 발생했는가

OSProfiler Redis backend에는 `osprofiler_opt:<base_id>` 형태의 Redis list가 있고, 각 entry는 start/stop raw event JSON이다. 처음에는 Go bridge가 Redis에서 이 list를 직접 읽어서 start/stop pair를 맞추고 OTLP span으로 만드는 선택지가 있었다.

### 오류 상황

실제 Redis sample을 보면 event order가 `LPUSH` 때문에 최신순이고, start/stop pair는 같은 `trace_id`를 공유하지만 여러 서비스와 host가 섞인다. 또한 DB span, WSGI span, 서비스 내부 span마다 payload shape이 다르다.

즉 Go에서 직접 파싱하면 다음 로직을 모두 직접 구현해야 했다.

```text
Redis key discovery
raw event ordering
start/stop pair matching
parent/child tree reconstruction
duration calculation
missing stop/start event handling
OSProfiler backend compatibility
```

### 사고 과정

OSProfiler CLI의 `trace show --json`이 이미 Redis driver를 사용해 report tree를 만들어낸다는 점이 핵심이었다. 우리가 필요한 것은 Redis raw parser가 아니라, 이미 만들어진 report JSON을 OTLP로 바꾸는 변환기였다.

### 해결

Go에서 Redis raw event를 직접 파싱하지 않기로 결정했다.

대신 Python helper가 OSProfiler storage driver를 호출한다.

```text
engine.get_report(base_id) -> OSProfiler report JSON
```

Go는 report JSON만 받아서 OTLP로 변환한다. 이 결정으로 구현 범위가 크게 줄었고, OSProfiler의 backend-specific 동작을 재구현하지 않아도 됐다.

## 2. `osprofiler trace show --json` CLI를 매번 호출할지 여부

### 왜 발생했는가

가장 쉬운 PoC는 Go가 매 trace마다 다음 명령을 subprocess로 실행하는 방식이었다.

```text
osprofiler trace show --json <base_id> --connection-string <redis-uri>
```

### 오류 상황

단건 PoC에는 충분하지만, watch mode에서 trace를 계속 polling하면 trace마다 Python interpreter와 OSProfiler CLI를 새로 띄우게 된다. 운영 형태에서는 비용이 커지고 timeout/error handling도 불편해진다.

### 사고 과정

OSProfiler report 생성은 Python library가 가장 안전하게 처리하고, long-running process control과 OTLP export는 Go가 담당하는 구조가 더 적합했다.

### 해결

long-running Python helper를 만들고 Go와 stdin/stdout NDJSON으로 통신하게 했다.

```json
{"id":"1","method":"get_report","base_id":"..."}
{"id":"1","ok":true,"report":{}}
```

이후 watch mode에서는 같은 helper process로 `list_traces`, `get_report`, `delete_trace`를 반복 호출한다.

## 3. helper 초기화 실패: `no such option profiler in group [DEFAULT]`

### 왜 발생했는가

초기 helper는 OSProfiler driver 초기화 시 OpenStack service config와 비슷한 형태를 기대하는 경로를 탔다. 하지만 bridge container는 OpenStack service container가 아니므로 `[profiler]` group 설정 파일이 존재하지 않는다.

### 오류 상황

컨테이너 실행 시 다음 오류가 발생했다.

```text
error: helper driver_init_failed: no such option profiler in group [DEFAULT]
```

### 사고 과정

bridge는 OpenStack 서비스 설정 전체를 읽을 필요가 없다. OSProfiler storage driver에 필요한 것은 Redis connection string이다. 따라서 helper가 oslo config를 서비스처럼 초기화하려는 방향은 불필요했다.

### 해결

helper에서 OSProfiler driver를 다음처럼 connection string 중심으로 초기화하도록 고쳤다.

```text
base.get_driver(connection_string, conf={})
```

이후 bridge container가 OpenStack 서비스 설정 파일 없이도 Redis backend report를 읽을 수 있게 됐다.

## 4. Tempo에서 trace 조회가 안 되는 혼동

### 왜 발생했는가

OSProfiler report JSON에는 `base_id`, `trace_id`, `parent_id`가 모두 등장한다. 이름만 보면 `trace_id`가 전체 trace ID처럼 보이지만, 실제로는 OSProfiler span/operation ID다.

### 오류 상황

Tempo API나 Grafana에서 `trace_id` 값으로 조회하면 원하는 trace가 나오지 않았다.

예를 들어 OSProfiler JSON의 값은 다음과 같았다.

```text
base_id  = 251bb5c1-30fb-4b04-a223-4410b831d4d7
trace_id = 149beffd-1985-4d29-ba30-bd597ed5a293
```

Tempo에서 조회해야 하는 값은 `trace_id`가 아니라 `base_id`에서 hyphen을 제거한 값이었다.

```text
251bb5c130fb4b04a2234410b831d4d7
```

### 사고 과정

OTel의 trace grouping 기준은 `TraceID` 하나다. OSProfiler에서는 전체 trace root가 `base_id`이고, 각 operation이 `trace_id`를 가진다.

### 해결

mapping을 명확히 고정했다.

```text
OSProfiler base_id   -> OTel TraceID
OSProfiler trace_id  -> OTel SpanID 생성용 원본값
OSProfiler parent_id -> OTel parentSpanId 생성용 원본값
```

문서와 테스트에서도 이 규칙을 기준으로 정리했다.

## 5. Grafana trace tree가 두 개의 root처럼 보이는 문제

### 왜 발생했는가

OSProfiler report의 top-level `total` node에는 `trace_id`가 없다. 실제 span은 `children` 아래에 있고, root WSGI span들의 `parent_id`는 `base_id`다.

### 오류 상황

처음 변환에서는 `base_id`를 parent span으로 해석할 실제 OTLP span이 없어서 Grafana에서 root span이 여러 개처럼 보였다.

예:

```text
wsgi
wsgi
  db
  db
```

### 사고 과정

Grafana trace UI가 하나의 coherent tree로 보여주려면 `base_id`에 해당하는 synthetic parent span이 필요했다.

### 해결

synthetic root span을 추가했다.

```text
span.name = osprofiler.total
span.attribute osprofiler.synthetic_root = true
```

그리고 `parent_id == base_id`인 span은 synthetic root 아래에 붙였다.

결과적으로 Grafana에서 다음처럼 보이게 됐다.

```text
osprofiler.total
  keystone.wsgi GET /
  keystone.wsgi POST /v3/auth/tokens
    keystone.db SELECT user
    keystone.db SELECT role
```

## 6. span 이름과 service resource가 너무 일반적인 문제

### 왜 발생했는가

초기 mapping은 span name을 OSProfiler의 `info.name`만 사용했다. 그러면 Grafana에서는 대부분 `wsgi`, `db`로만 보였다.

### 오류 상황

Trace는 들어오지만 사용자가 실제로 무엇이 느린지 보기 어려웠다.

```text
wsgi
db
db
db
```

### 사고 과정

OSProfiler raw payload 안에는 HTTP method/path, DB statement, project, host가 들어 있었다. 이를 display name과 resource attribute에 반영해야 Grafana UI에서 바로 의미가 보인다.

### 해결

span name과 resource를 개선했다.

```text
resource.service.name = keystone / nova / neutron / glance
service.instance.id   = aolda-compute / aolda-control
span.name             = keystone.wsgi POST /v3/auth/tokens
span.name             = nova.db SELECT flavors
span.name             = neutron.wsgi GET /v2.0/ports
```

DB params는 기본 redaction하고, SQL statement는 span name용 summary만 뽑도록 했다.

## 7. 단건 export만 있고 지속 처리되지 않는 문제

### 왜 발생했는가

초기 MVP는 명시적으로 `--base-id` 하나를 받아 export하는 구조였다. 하지만 실제 운영에서는 Redis에 OSProfiler trace가 계속 쌓인다.

### 오류 상황

Portainer로 상시 배포하려고 하자 다음 문제가 드러났다.

```text
error: --base-id is required
```

즉 container가 daemon처럼 돌지 않고 단건 CLI처럼 종료됐다.

### 사고 과정

운영에서는 bridge가 Redis trace 목록을 주기적으로 가져와 batch로 처리해야 한다. 다만 과부하를 막기 위해 polling interval, export delay, max traces per poll이 필요했다.

### 해결

`watch` command를 추가했다.

```text
osprofiler-tempo-bridge watch --config /etc/osprofiler-tempo-bridge/config.yaml
```

watch mode는 다음 순서로 동작한다.

```text
list_traces
skip too-new traces by export_delay
get_report
convert
export
record local state
delete Redis trace after success
```

Redis 삭제는 OTLP export 성공 이후에만 수행한다. export 실패 시 Redis data는 보존된다.

## 8. 성공 export 후 Redis cleanup과 중복 export 문제

### 왜 발생했는가

운영에서는 Redis가 계속 커지지 않도록 성공한 trace를 삭제하고 싶었다. 하지만 단순히 삭제만 하면 delete 실패 시 중복 export 또는 data loss가 생길 수 있다.

### 오류 상황

필요한 동작은 다음처럼 복합적이었다.

```text
OTLP export 성공 -> Redis 삭제
OTLP export 실패 -> Redis 유지
Redis 삭제 실패 -> 다음 poll에서 삭제 재시도
이미 export한 trace -> 다시 export하지 않음
```

### 사고 과정

export 성공 여부와 Redis 삭제 성공 여부는 분리해서 기록해야 했다. 삭제 실패가 export 재시도를 유발하면 Tempo에 duplicate span이 쌓일 수 있다.

### 해결

local state file을 추가했다.

```text
/var/lib/osprofiler-tempo-bridge/state.json
```

state에는 exported 여부와 delete pending 여부를 기록한다. 그래서 성공 export 후 delete가 실패해도 다음 poll에서는 export를 반복하지 않고 deletion만 재시도한다.

## 9. long-running helper에서 `int`와 `datetime` subtraction 오류

### 왜 발생했는가

OSProfiler Redis driver의 `get_report()`는 내부 상태를 일부 mutation한다. CLI처럼 매번 새 process를 띄우면 문제가 안 보이지만, long-running helper에서 같은 driver instance로 여러 report를 읽으면 이전 report 상태가 다음 report에 영향을 줬다.

### 오류 상황

watch mode에서 반복적으로 다음 오류가 발생했다.

```text
watch export_failed base_id=... error=helper get_report_failed:
unsupported operand type(s) for -: 'int' and 'datetime.datetime'
```

### 사고 과정

단건 export는 성공하고 watch mode에서만 실패했으므로, Redis data 자체 문제라기보다 helper process reuse 문제로 봤다. OSProfiler driver 내부에 report calculation state가 남는다고 판단했다.

### 해결

각 `get_report()` 호출 전에 driver의 report-related state를 reset했다.

```text
result
started_at
finished_at
last_started_at
```

그리고 helper unit test를 추가해서 같은 helper process에서 여러 report를 읽는 경우를 검증했다.

## 10. config/env 주입 혼동

### 왜 발생했는가

config는 YAML과 env override를 같이 지원한다. Portainer 배포에서는 config file mount, env, command가 모두 맞아야 한다.

### 오류 상황

대표 오류는 다음과 같았다.

```text
error: osprofiler.connection_string is required
```

또는 export command를 사용해서:

```text
error: --base-id is required
```

### 사고 과정

운영 배포에서는 단건 export가 아니라 watch command를 써야 하고, secret은 config file이 아니라 env로 넣는 것이 맞다.

### 해결

Portainer 기준 실행 형태를 고정했다.

```text
image: ghcr.io/aolda/aolda-trace-bridge:latest
command: watch --config /etc/osprofiler-tempo-bridge/config.yaml
env:
  OSPROFILER_CONNECTION_STRING=redis://...
  OTLP_ENDPOINT=http://otel-collector:4318/v1/traces
volume:
  state volume -> /var/lib/osprofiler-tempo-bridge
```

## 11. GHCR pull unauthorized와 CI image publish

### 왜 발생했는가

초기 GHCR package visibility가 private이어서 target environment에서 pull이 실패했다.

### 오류 상황

```text
docker: Error response from daemon:
Head "https://ghcr.io/v2/aolda/aolda-trace-bridge/manifests/latest": unauthorized
```

### 사고 과정

운영 환경에서 별도 GHCR token 없이 pull하려면 package를 public으로 열어야 한다. 또한 CI가 매번 image를 build/push해야 배포자가 `latest`를 pull할 수 있다.

### 해결

GHCR package를 public으로 전환하고, GitHub Actions에서 test 후 image를 publish하도록 했다.

이후 CI 속도 문제도 확인했고, cache를 활용해 build 시간을 줄였다.

## 12. request_id 기반 Loki-Tempo 연결 한계

### 왜 발생했는가

OpenStack log에는 `request_id`가 잘 찍힌다. 하지만 OSProfiler report JSON에는 기본적으로 request_id가 들어오지 않았다.

### 오류 상황

Loki derived field에서 다음 TraceQL을 사용해도 결과가 없었다.

```text
{ span.openstack.request_id = "76c761b9-fc76-4e07-b878-c5d2b65e5c80" }
```

### 사고 과정

bridge는 OSProfiler JSON에 있는 값만 Tempo span attribute로 올릴 수 있다. OpenStack log에 있는 request_id를 bridge가 나중에 알 방법은 없다.

따라서 연결하려면 둘 중 하나가 필요하다.

```text
OpenStack OSProfiler WSGI payload에 request_id를 넣는다.
또는 HAProxy에서 trace base_id를 만들고 access log에도 남긴다.
```

둘 다 OpenStack service image, middleware, HAProxy Lua/log format 같은 별도 영역을 건드린다.

### 해결

bridge 쪽은 request_id가 payload에 들어오면 바로 attribute로 승격하도록 준비했다.

지원하는 형태:

```text
request_id
global_request_id
x-openstack-request-id
x-compute-request-id
request.id
req-<uuid> 문자열
```

하지만 현재 MVP scope에서는 OpenStack log와 Tempo trace의 자동 correlation은 제외했다. 이유는 목표 대비 OpenStack/HAProxy 내부 개조 비용이 너무 크기 때문이다.

## 13. JSON payload 저장 방식에 대한 정리

### 왜 발생했는가

운영 시 OSProfiler report JSON을 bridge가 파일로 계속 남기는지, 민감한 SQL params가 저장되는지 확인이 필요했다.

### 오류 상황

OSProfiler payload에는 SQL statement와 params가 포함될 수 있다. 이를 그대로 파일이나 로그에 남기면 위험하다.

### 사고 과정

bridge는 변환기가 되어야지, trace archive가 되면 안 된다. 장기 저장은 Tempo/Collector 쪽 책임으로 두는 것이 낫다.

### 해결

bridge는 report JSON을 파일로 저장하지 않는다. helper에서 받은 JSON은 Go process memory에서 OTLP로 변환한 뒤 버린다.

단, span attribute에는 redacted `osprofiler.info_json`을 넣는다.

기본 redaction:

```text
db.params -> redacted
sensitive key names -> redacted
```

## 최종 결론

구현 중 가장 큰 판단은 다음 세 가지였다.

```text
Redis raw parser를 만들지 않는다.
OSProfiler Python driver를 helper로 감싼다.
Go는 OTLP 변환/export/watch/state에 집중한다.
```

현재 MVP는 다음 기준을 만족한다.

```text
Redis에 쌓인 OSProfiler trace를 주기적으로 읽는다.
OSProfiler report JSON을 OTLP span tree로 변환한다.
Tempo/Grafana에서 trace tree를 볼 수 있다.
성공 export 후 Redis trace를 삭제할 수 있다.
컨테이너 이미지와 CI/GHCR publish가 동작한다.
```

의도적으로 남긴 한계는 다음이다.

```text
OpenStack log line에서 Tempo trace로 자동 점프하는 correlation은 MVP 범위 밖이다.
```

이 한계는 bridge 내부 문제가 아니라 OpenStack log의 `request_id`와 OSProfiler의 `base_id`가 기본적으로 같은 저장소/로그에 같이 존재하지 않는 구조에서 온다. 필요해지면 별도 2차 작업으로 HAProxy trace header/log injection 또는 OpenStack WSGI/logging 패치를 검토하는 것이 맞다.
