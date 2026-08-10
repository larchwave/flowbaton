# Third-Party Notices

This file records third-party material committed to the repository, linked by
the current FlowBaton build, or pinned as build-only tooling. It does not replace
the release SBOM and license audit required before distribution.

The JavaScript runtime described below is part of the Apache-2.0 FlowBaton
distribution. Every public release still requires the release SBOM,
third-party-license inventory, and resolved-license reconciliation described below.

## Go host runtime dependencies

| Component | Version/source | License | FlowBaton role and integrity evidence |
| --- | --- | --- | --- |
| go-yaml v3 | `go.yaml.in/yaml/v3` v3.0.4, <https://github.com/yaml/go-yaml>, commit `c3552c15f996075a7634df5159d9161c67bf3d76` | MIT and Apache License 2.0 | YAML Node API used by the host parser for typed decoding and source positions. Module sum: `h1:tfq32ie2Jv2UxXFdLJdh3jXuOzWiL1fo0bu/FbuKpbc=`. `go.mod` sum: `h1:DhzuOOF2ATzADvBadXxruRBLzYTpT36CKvDb3+aBEFg=`. `LICENSE` SHA-256: `d18f6323b71b0b768bb5e9616e36da390fbd39369a81807cca352de4e4e6aa0b`. |
| goja | `github.com/dop251/goja` `v0.0.0-20260603125802-cfe4039cb6d7`, <https://github.com/dop251/goja>, commit `cfe4039cb6d77b297d8b637182f774fa4a54b7d5` (declares `go 1.20`) | MIT; Lucent permissive notice for bundled `ftoa` material | Shared strict ECMAScript runtime behind the Go-native `internal/js` API. Module sum: `h1:g7JYcX9Y5RNJhrqpC/N/vHtp9BuFU1MVc4BO64scbKo=`. `go.mod` sum: `h1:MxLav0peU43GgvwVgNbLAj1s/bSGboKkhuULvq/7hx4=`. `LICENSE` SHA-256: `8a1266c1dd7d22027455bea83b92087f606c8d5fc701b269a8411f008ed49a99`. `ftoa/LICENSE_LUCENE` SHA-256: `eb4df27f95a096a23d88e567331a4bee5590d22185942a1b0456197e29d03e59`. |
| regexp2 | `github.com/dlclark/regexp2` v1.11.4, <https://github.com/dlclark/regexp2> | MIT | Compiled goja transitive for ECMAScript regular-expression semantics. Module sum: `h1:rPYF9/LECdNymJufQKmri9gV604RvvABwgOA8un7yAo=`. `go.mod` sum: `h1:DHkYz0B9wPfa6wondMfaivmHpzrQ3v9q8cnmRbL6yW8=`. `LICENSE` SHA-256: `9be5d04bb4d706914d5bf943710da4afeb42048f7c529902fb57c82762a991a9`. |
| go-sourcemap | `github.com/go-sourcemap/sourcemap` v2.1.3+incompatible, <https://github.com/go-sourcemap/sourcemap> | BSD-2-Clause | Compiled goja transitive for JavaScript source maps. Module sum: `h1:W1iEw64niKVGogNgBN3ePyLFfuisuzeidWPMPWmECqU=`. `go.mod` sum: `h1:F8jJfvm2KbVjc5NqelyYJmf/v5J0dwNLS2mL4sNA1Jg=`. `LICENSE` SHA-256: `92e002c4f78b8f80445cf72c4bd33d78e4998371a5eff80886c5648a7974a19f`. |
| google/pprof | `github.com/google/pprof` v0.0.0-20230207041349-798e818bf904, <https://github.com/google/pprof> | Apache License 2.0 | Compiled goja transitive providing profile data types. Module sum: `h1:4/hN5RUoecvl+RmJRE2YxKWtnnQls6rQjjW5oV7qg2U=`. `go.mod` sum: `h1:uglQLonpP8qtYCYyzA+8c/9qtqgA3qsXGYqCPKARAFg=`. `LICENSE` SHA-256: `cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30`. |
| golang.org/x/net | `golang.org/x/net` v0.50.0, <https://go.googlesource.com/net> (GitHub mirror <https://github.com/golang/net>), tag `v0.50.0`, commit `ebddb99633e0fc35d135f62e9400678492c1d3be` | BSD-3-Clause | h2c HTTP/2 client engine under FlowBaton's gRPC frame and trailer layer in `internal/android/grpcwire` (Android agent transport). Module sum: `h1:ucWh9eiCGyDR3vtzso0WMQinm2Dnt8cFMuQa9K33J60=`. `go.mod` sum: `h1:UgoSli3F/pBgdJBHCTc+tp3gmrU4XswgGRgtnwWTfyM=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| golang.org/x/text | `golang.org/x/text` v0.34.0, <https://pkg.go.dev/golang.org/x/text> | BSD-3-Clause | Compiled transitive of goja and of `golang.org/x/net` (idna) for Unicode and language support; required by x/net v0.50.0. Module sum: `h1:oL/Qq0Kdaqxa1KbNeMKwQq0reLCCaFtqu2eNuSeNHbk=`. `go.mod` sum: `h1:homfLqTYRFyVYemLBFl5GgL/DWEiH5wcsQ5gSh1yziA=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| MCP Go SDK | `github.com/modelcontextprotocol/go-sdk` v1.7.0, <https://github.com/modelcontextprotocol/go-sdk>, tag `v1.7.0` | Apache-2.0 (transitioning MIT→Apache-2.0) | Official MCP SDK behind the `flowbaton mcp` stdio server. Module sum: `h1:yqjY2dsbKAC0LSuWZVBMrHgiG8ukXv6NRo0JiALay44=`. `go.mod` sum: `h1:dL7u98E/zjJTGzEq+j30jQ8K2k1mb6LeAH4inEcSGts=`. `LICENSE` SHA-256: `af679003d933f045393a6a029f43da113f9ae364eac651d9ae268392985580f5`. |
| jsonschema-go | `github.com/google/jsonschema-go` v0.4.3, <https://github.com/google/jsonschema-go> | MIT | Compiled MCP transitive: tool JSON Schema generation/validation for `mcp.AddTool`. Module sum: `h1:/DBOLZTfDow7pe2GmaJNhltueGTtDKICi8V8p+DQPd0=`. `go.mod` sum: `h1:r5quNTdLOYEz95Ru18zA0ydNbBuYoo9tgaYcxEYhJVE=`. `LICENSE` SHA-256: `2d56c53449691d85d9aea245eb8dac12713e9075d70d5557b82ae1e94805b357`. |
| segmentio/encoding | `github.com/segmentio/encoding` v0.5.4, <https://github.com/segmentio/encoding> | MIT | Compiled MCP transitive: fast JSON for JSON-RPC bodies. Module sum: `h1:OW1VRern8Nw6ITAtwSZ7Idrl3MXCFwXHPgqESYfvNt0=`. `go.mod` sum: `h1:HS1ZKa3kSN32ZHVZ7ZLPLXWvOVIiZtyJnO1gPH1sKt0=`. `LICENSE` SHA-256: `d6d71a1f7dc6539e371120cc7af6e3257e55ca79634d473211f217b8965b0f16`. |
| segmentio/asm | `github.com/segmentio/asm` v1.1.3, <https://github.com/segmentio/asm> | MIT | Compiled MCP transitive: SIMD helpers for `segmentio/encoding`. Module sum: `h1:WM03sfUOENvvKexOLp+pCqgb/WDjsi7EK8gIsICtzhc=`. `go.mod` sum: `h1:Ld3L4ZXGNcSLRg4JBsZ3//1+f/TjYl0Mzen/DQy1EJg=`. `LICENSE` SHA-256: `e2a78de21d6d8ded2dff0f3189cd32e011630d785da127ebfbc8949012c0947b`. |
| yosida95/uritemplate | `github.com/yosida95/uritemplate/v3` v3.0.2, <https://github.com/yosida95/uritemplate> | BSD-3-Clause | Compiled MCP transitive: RFC 6570 URI templates for resource templates. Module sum: `h1:Ed3Oyj9yrmi9087+NczuL5BwkIc4wvTb5zIM+UJPGz4=`. `go.mod` sum: `h1:ILOh0sOhIJR3+L/8afwt/kE++YT040gmv5BQTMR2HP4=`. `LICENSE` SHA-256: `0761aadfb1921103752869ee942d4a71bdd54494697684d4b13dc17ad9781191`. |
| golang.org/x/oauth2 | `golang.org/x/oauth2` v0.35.0, <https://pkg.go.dev/golang.org/x/oauth2> | BSD-3-Clause | Compiled MCP transitive: OAuth2 types on the SDK's auth-capable protocol structs. OAuth is not used by the stdio server. Module sum: `h1:Mv2mzuHuZuY2+bkyWXIHMfhNdJAdwW3FuWeCPYN5GVQ=`. `go.mod` sum: `h1:lzm5WQJQwKZ3nwavOZ3IS5Aulzxi68dUSgRHujetwEA=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| golang.org/x/sync | `golang.org/x/sync` v0.20.0, <https://pkg.go.dev/golang.org/x/sync> | BSD-3-Clause | Compiled MCP transitive: `errgroup` for concurrent session handling. Module sum: `h1:e0PTpb7pjO8GAtTs2dQ6jYa5BWYlMuX047Dco/pItO4=`. `go.mod` sum: `h1:9xrNwdLfx4jkKbNva9FpL6vEN7evnE43NNNJQ2LF3+0=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| golang.org/x/time | `golang.org/x/time` v0.15.0, <https://pkg.go.dev/golang.org/x/time> | BSD-3-Clause | Compiled MCP transitive: `rate` limiter for the SDK transports. Module sum: `h1:bbrp8t3bGUeFOx08pvsMYRTCVSMk89u4tKbNOZbp88U=`. `go.mod` sum: `h1:Y4YMaQmXwGQZoFaVFk4YpCt4FLQMYKZe9oeV/f4MSno=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| golang.org/x/sys | `golang.org/x/sys` v0.41.0, <https://pkg.go.dev/golang.org/x/sys> | BSD-3-Clause | Compiled MCP transitive: low-level syscalls for `segmentio/asm` CPU detection (graph edge before the MCP path). Module sum: `h1:Ivj+2Cp/ylzLiEU89QhWblYnOE9zerudt9Ftecq2C6k=`. `go.mod` sum: `h1:OgkHotnGiDImocRcuBABYBEXf8A9a87e/uXjp9XT3ks=`. `LICENSE` SHA-256: `911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad`. |
| langchaingo | `github.com/tmc/langchaingo` v0.1.14, <https://github.com/tmc/langchaingo>, tag `v0.1.14`, commit `509308ff01c13e662d5613d3aea793fabe18edd2` (declares `go 1.24`) | MIT | Provider-agnostic multimodal LLM client (`llms.Model`, OpenAI + Anthropic) used by the AI screenshot engine for `assertWithAI` / `assertNoDefectsWithAI` / `extractTextWithAI`. Module sum: `h1:o1qWBPigAIuFvrG6cjTFo0cZPFEZ47ZqpOYMjM15yZc=`. `go.mod` sum: `h1:aKKYXYoqhIDEv7WKdpnnCLRaqXic69cX9MnDUk72378=`. `LICENSE` SHA-256: `0ac223aad8ac9f5331b7a6c7161b7011fd42907940964489985274ecda0e5dcf`. |
| pgx | `github.com/jackc/pgx/v5` v5.10.0, <https://github.com/jackc/pgx>, tag `v5.10.0`, commit `7293fb11125be0373a92f716683f2d494f6fd4b0` | MIT | PostgreSQL transactions, connection pooling, migrations, durable lease fencing, identity mappings, requests, and events for the distributed DeviceSession runtime. Module sum: `h1:VhSvgU2jSli8o3AqIEOTJr7rZwAEUVo4E4XhR94Zfr0=`. `go.mod` sum: `h1:mal1tBGAFfLHvZzaYh77YS/eC6IX9OWbRV1QIIM0Jn4=`. `LICENSE` SHA-256: `467f95e074fe23079a5623ed652619682692041b8551da27e3c2ddb9659a1507`. |
| pgpassfile | `github.com/jackc/pgpassfile` v1.0.0 | MIT | pgx connection configuration support for PostgreSQL password files. Module sum: `h1:/6Hmqy13Ss2zCq62VdNG8tM1wchn8zjSGOBJ6icpsIM=`. `LICENSE` SHA-256: `adb1663fda031df8f4344aa68f299fd87d80353e31339406742ded21dae65702`. |
| pgservicefile | `github.com/jackc/pgservicefile` `v0.0.0-20240606120523-5a60cdf6a761` | MIT | pgx connection configuration support for PostgreSQL service files. Module sum: `h1:iCEnooe7UlwOQYpKFhBabPMi4aNAfoODPEFNiAnClxo=`. `LICENSE` SHA-256: `fc505773403fe869ed64cc2235cdd13988a427bb7e3a7e7004a3f4b27420f8fc`. |
| puddle | `github.com/jackc/puddle/v2` v2.2.2 | MIT | Connection-pool implementation compiled through pgxpool. Module sum: `h1:PR8nw+E/1w0GLuRFSmiioY6UooMp6KJv0/61nB7icHo=`. `LICENSE` SHA-256: `2d50e98a4900b4d6457a38d39c1432fdc156fc2f7b365f2e33ec9344acbb0057`. |
| google/uuid | `github.com/google/uuid` v1.6.0, <https://github.com/google/uuid>, tag `v1.6.0`, commit `0f11ee6918f41a04c201eceeadf612a377bc7fbc` | BSD-3-Clause | Compiled langchaingo transitive: UUIDs in the OpenAI/Anthropic provider clients. Module sum: `h1:NIvaJDMOsjHA8n1jAhLSgzrAzy1Hgr+hNrb57e+94F0=`. `go.mod` sum: `h1:TIyPZe4MgqvfeYDBFedMoGGpEw/LqOeaOT+nhxU+yHo=`. `LICENSE` SHA-256: `0a8d61ed3cbfd5312326e8126c31ce9c627a283adc99131b56896d29ada04b2d`. |
| pkoukk/tiktoken-go | `github.com/pkoukk/tiktoken-go` v0.1.6, <https://github.com/pkoukk/tiktoken-go>, tag `v0.1.6`, commit `475cdcdb453a623bd4040bcc88071c54b0c94e07` | MIT | Compiled langchaingo transitive: BPE token counting. Module sum: `h1:JF0TlJzhTbrI30wCvFuiw6FzP2+/bR+FIxUdgEAcUsw=`. `go.mod` sum: `h1:9NiV+i9mJKGj1rYOT+njbv+ZwA/zJxYdewGl6qVatpg=`. `LICENSE` SHA-256: `f6ec360d772e920cc1f85c38e15d013785efc860914e0d33d1c63018335eac0b`. |

`go list -deps ./internal/js` confirms that goja, regexp2, go-sourcemap,
google/pprof, and golang.org/x/text are the external modules whose packages
compile into the JS runtime path. `go list -deps ./internal/android/grpcwire`
confirms that golang.org/x/net and golang.org/x/text are the external modules
whose packages compile into the Android gRPC transport path.
`go list -deps ./cmd/flowbaton` confirms the MCP SDK and its compiled
transitives above (jsonschema-go, segmentio/encoding, segmentio/asm,
uritemplate, x/oauth2, x/sync, x/time, x/sys) enter the binary through the
`flowbaton mcp` server; `github.com/golang-jwt/jwt/v5` is a graph edge only and
is not compiled by the stdio server.
`go list -deps ./internal/aiengine` confirms langchaingo and its two new
compiled transitives (google/uuid, pkoukk/tiktoken-go) enter through the AI
screenshot engine; `github.com/dlclark/regexp2` is reached here too but is
already listed above as a goja transitive (MVS keeps the single v1.11.4).
`go list -deps ./internal/server` confirms pgx, pgpassfile, pgservicefile, and
puddle enter the distributed runtime through `internal/sessionstore`.
The wider `go list -m all` graph also contains dependency-module test and tool
edges; those are recorded separately in `docs/dependency-policy.md` and are not
represented here as linked runtime components.

## Build tooling

| Component | Version/source | License | Repository and distribution role |
| --- | --- | --- | --- |
| Gradle Wrapper | 8.5, <https://github.com/gradle/gradle> | Apache License 2.0 | The official `gradlew`, `gradlew.bat`, and `gradle-wrapper.jar` are committed solely to bootstrap Android builds. The JAR SHA-256 is `d3b261c2820e9e3d8d639ed084900f11f4a86050a8f83342ade7b6bc9b0d2bdd` and it contains `META-INF/LICENSE`. The Gradle 8.5 binary distribution is downloaded as a build tool, not linked into FlowBaton; its pinned SHA-256 is recorded in `gradle-wrapper.properties`. |
| XcodeGen | 2.44.1, <https://github.com/yonaskolb/XcodeGen/releases/tag/2.44.1> | MIT | Build-only Xcode project generator downloaded by CI. The release archive SHA-256 is `a2e905fb68446e9bb4008cdfe2e13e3f176d0cbcca828b71770f8e53fca91b73`. XcodeGen is not linked into or shipped with FlowBaton. |

## Android build and runtime graph

| Component | Version/source | License | FlowBaton role |
| --- | --- | --- | --- |
| Android Gradle Plugin | 8.3.2, <https://android.googlesource.com/platform/tools/base/> | Apache License 2.0 | Build-time application and instrumentation packaging only. |
| Kotlin Gradle plugin and standard library | 1.9.22, <https://github.com/JetBrains/kotlin> | Apache License 2.0 | Build plugin; Kotlin standard-library code is part of the debug Android runtime graph. |
| AndroidX Test Runner / AndroidX Test JUnit | 1.5.2 / 1.1.5, <https://android.googlesource.com/platform/frameworks/support/> | Apache License 2.0 | Instrumentation test APK only; not part of the Go host. |
| gRPC Java API, stub, and shaded Netty transport | 1.81.0, <https://github.com/grpc/grpc-java> | Apache License 2.0 | Plaintext loopback server runtime in the debug Android agent APK. |
| JUnit 4 | 4.13.2, <https://github.com/junit-team/junit4> | Eclipse Public License 1.0 | JVM and instrumentation test dependency; not part of the Go host. |

The resolved direct and transitive Android artifacts are recorded in
`drivers/android/core/gradle.lockfile`,
`drivers/android/agent/gradle.lockfile`, and
`drivers/android/gradle/verification-metadata.xml`. Generated APKs are ignored
debug artifacts and are excluded from distribution. Distribution requires the
resolved graph in the release SBOM and all applicable license and notice texts.

## CI and release tooling

GitHub Actions, GoReleaser, and Syft are pinned build services/tools. They are
not linked into or shipped as FlowBaton runtime components. Their versions and
immutable action revisions are recorded in the dependency policy and workflow
files.
