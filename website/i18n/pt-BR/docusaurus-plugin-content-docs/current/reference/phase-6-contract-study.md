---
sidebar_position: 22
---

# Fase 6 — estudo de contratos

**Data:** 2026-11-16. **Base de código:**
`970b7f141a4eb7eb0865865191acd3d4ee53a9b7`, mais as alterações documentais
pendentes. **Resultado:** estudo concluído; correções de contrato e aceitação da
decisão continuam abertas. ADR-0007 permanece **Proposto**. O ensaio original não alterou runtime; a correção posterior do exportador está
registrada abaixo.

## Recomendação

Manter codegen adiado. Priorizar a correção do contrato de PATCH e a descrição
explícita das restrições exclusivas do servidor. O fixture equivalente YAML/Go
compilou para schemas de runtime iguais; geração não corrige divergências entre
exportador OpenAPI e validação. Dois exemplos existentes não demonstram custo
recorrente de DTOs nem justificam um gerador de produção.

Isto é evidência de correção, não benchmark de desempenho ou produtividade.
Não há economia de tempo medida, consumidor real de IA nem política de autorização
aprovada. O estudo não encerra todos os critérios da Fase 6: a comparação de
contratos ainda tem contraexemplos sem correção.

## Método e limites

- Origem YAML: `examples/standalone-tags-api/resources.yaml`, lido diretamente
  e compilado por `declarative.Parse` e `Resource.Build(nil)`: três campos de
  primeiro nível, dois obrigatórios, um único e um default. YAML continua sendo
  a origem autoral; o setup Go acrescenta auditoria separadamente.
- Origem Go: `commentsSchema` extraído literalmente de
  `examples/blog-apis/services/comments-api/comments.go`: quatro campos de
  primeiro nível, três obrigatórios e um default. Go continua sendo a origem
  autoral. O ensaio não importa nem inicia o pacote `main` da aplicação.
- Esses exemplos não cobrem objetos aninhados, listas, campos obrigatórios
  nullable ou callbacks. Um fixture explicitamente sintético `probe`, com oito
  campos de primeiro nível, cobre esses casos. Seu schema YAML e equivalente Go
  manual são iguais por `reflect.DeepEqual` antes de adicionar callbacks Go.
  Essa duplicação é instrumentação do estudo, não um segundo schema de produção.
- Os três recursos usam SQLite em memória isolado, com o handler REST, `Service`
  e adapter `restsqlite` reais. Requests passam por `httptest` no processo, sem
  listener TCP, Mongo, Postgres, Pub/Sub ou banco de produção. O hook de auditoria
  tags, o outbox comments, auth e bootstrap originais não são exercitados. Isso
  valida o schema selecionado, não os serviços completos.
- O ensaio lê `/api/openapi.json`, compila o componente Input/Patch gerado com
  `jsonschema/v6` e valida o mesmo JSON enviado ao HTTP. Mutações bem-sucedidas
  são lidas para verificar defaults/exclusão. PATCH usa o ETag concreto mais
  recente e `application/merge-patch+json`.
- O ensaio habilita GET/PATCH/PUT por item e exigência de merge-patch uniformemente.
  Essas escolhas experimentais não descrevem a configuração original inalterada
  dos exemplos.
- Defaults em JSON Schema são anotações, não normalização. Checks dependentes de
  estado e extensões não são executados automaticamente por `jsonschema/v6`.

## Resultados observados

São 21 casos de mutação: 15 concordam em aceitar/rejeitar; seis são aceitos por
JSON Schema e rejeitados por HTTP. Oito mutações bem-sucedidas foram lidas de volta.
Os casos são sequenciais por recurso; rejeições preservam o último estado/ETag
bem-sucedido. Todas as divergências retornaram 422 com erros de validação, não
falhas de ETag ausente ou obsoleto.

| Recurso | Caso | JSON enviado | JSON Schema | HTTP |
|---|---|---|---|---|
| tags | `required missing` | `{}` | rejeita | 422 |
| tags | `required null` | `{"name":null,"slug":"x"}` | rejeita | 422 |
| tags | `default applied` | `{"name":"First","slug":"first"}` | aceita | 201 |
| tags | `unknown field` | `{"extra":true,"name":"Other","slug":"other"}` | rejeita | 422 |
| tags | `duplicate unique` | `{"name":"Duplicate","slug":"first"}` | aceita | 422 |
| tags | `patch omit required` | `{"enabled":false}` | aceita | 200 |
| tags | `patch remove required` | `{"name":null}` | rejeita | 422 |
| comments | `required missing` | `{}` | rejeita | 422 |
| comments | `default applied` | `{"author_name":"A","body":"hello","post_id":"p"}` | aceita | 201 |
| comments | `status unconstrained` | `{"status":"not-a-real-status"}` | aceita | 200 |
| comments | `patch omit required` | `{"body":"edited"}` | aceita | 200 |
| probe | `nullable create` | `{"labels":["a"],"name":"one","optional":null,"profile":{"label":"L"},"required_null":null,"rows":[{"label":"R"}]}` | aceita | 201 |
| probe | `list wrong item` | `{"labels":[12]}` | rejeita | 422 |
| probe | `nested required omitted` | `{"profile":{}}` | aceita | 422 |
| probe | `list object required omitted` | `{"rows":[{}]}` | aceita | 422 |
| probe | `field callback` | `{"checked":"deny"}` | aceita | 422 |
| probe | `resource callback` | `{"checked":"resource-deny"}` | aceita | 422 |
| probe | `required nullable deletion` | `{"required_null":null}` | aceita | 422 |
| probe | `optional null deletion` | `{"optional":null}` | aceita | 200 |
| probe | `nullable create omitted required` | `{"name":"two"}` | rejeita | 422 |
| probe | `nested default` | `{"name":"two","profile":{"label":"L"},"required_null":"v"}` | aceita | 201 |

Defaults persistidos: `tags.enabled=true`, `comments.status="pending"`,
`probe.enabled=true` e `probe.profile.active=true`. Omitir campos obrigatórios
no primeiro nível de PATCH preservou valores anteriores. Um campo opcional
nullable armazenou `null` em POST e perdeu a chave em PATCH RFC 7396. Um campo
obrigatório nullable aceitou `null` em POST; PATCH tentou excluir a chave e falhou.

O schema comments aceita `status="not-a-real-status"` tanto em OpenAPI quanto no
HTTP: há default, mas não enumeração `Allowed`. Isso concorda com o contrato
existente; não comprova que qualquer status satisfaça regras de negócio. Cabe à
aplicação decidir se deve restringi-lo.

## Achados e encaminhamento

### C6-01 — Obrigatoriedade aninhada relaxada demais no PATCH

**Divergência de contrato reproduzida.** Com
`profile={"label":"L","active":true}` armazenado, PATCH `{"profile":{}}` é
aceito pelo schema Patch, mas retorna 422 exigindo `profile.label`. Do mesmo modo,
`{"rows":[{}]}` passa no schema e falha por ausência de `label` no item.

`rest/openapi.go` propaga `partial=true` recursivamente para objetos e itens de
lista. Em `rest/schema.go`, `validateValue` valida objetos enviados com
`partial=false`, inclusive itens de lista. O validador rejeita antes do merge
SQLite. Não se afirma que as integrações Mongo/Postgres tenham sido executadas.

**Correção recomendada:** manter opcionalidade no primeiro nível de PATCH e
alinhar o contrato à validação aninhada real. No mínimo, itens de lista que
substituem valores precisam preservar seus obrigatórios. Acrescentar regressões
comparando o componente com validação real. Decidir explicitamente se a própria
semântica dos objetos aninhados deve mudar; descrever o contrato atual é a opção
menor e preserva compatibilidade. Nenhuma correção foi implementada no estudo.

### C6-02 — Nullable não descreve exclusão via merge patch

**Restrição de operação/backend reproduzida.** Uma string obrigatória nullable
aceita `null` no componente e em POST, mas PATCH retorna 422: RFC 7396 remove a
chave e `ValidatesMergedDocument()` exige sua presença. Um campo opcional nullable
é removido pelo mesmo PATCH com sucesso.

**Correção recomendada:** documentar entrada versus estado persistido por operação
e backend; avaliar schema/anotações específicos de exclusão por merge patch.
Não emitir regra universal de null em PATCH: o merge legado Mongo é diferente.
Não enfraquecer o HTTP para fazê-lo caber no schema genérico.

### C6-03 — Unicidade depende do estado do servidor

**Aproximação esperada, reproduzida.** `tags.slug` duplicado passa no JSON Schema
e retorna 422. `x-hyrule-unique=true` já sinaliza a regra. JSON Schema sozinho
não consulta dados nem resolve corrida entre inserts concorrentes.

**Encaminhamento:** preservar a extensão e explicar a execução no servidor;
manter enforcement no adapter/banco. Geração não resolve unicidade.

### C6-04 — Callbacks continuam exclusivos do servidor

**Aproximação esperada, reproduzida.** `checked="deny"` falha em `Field.Check`;
`checked="resource-deny"` falha em `Resource.Validate`. Ambas as strings passam
no schema estrutural. `Field.Check` tem `x-hyrule-custom-validation=true`;
a validação de recurso tem apenas o aviso genérico da operação sobre regras
adicionais de callbacks/hooks, sem marcador específico de recurso.

**Correção recomendada:** avaliar marcador neutro explícito para
`Resource.Validate` e separar validação estrutural da validação autoritativa do
servidor. Não serializar closures Go nem gerar validadores cliente que prometam
reproduzi-las.

### C6-05 — Diagnósticos YAML podem melhorar antes de codegen

Um fixture inicial de lista usou `schema: {type: string}` onde Hyrule exige
`items: {type: string}`. O parser falhou com localização e mensagens como
`cannot unmarshal !!str ... into declarative.Field`. Era um fixture inválido do
ensaio, não falha de recurso válido. Corrigir para `items`, como documentado,
permitiu executar a comparação e os casos.

**Encaminhamento:** avaliar mensagens que identifiquem recurso/campo e a sintaxe
`items` esperada. O parser já fornece localização; não é necessário um gerador
ou linguagem nova apenas para melhorar essa mensagem.

## Evidência de DX e gatilho de codegen

As duas origens reais somam sete definições de campos de primeiro nível. Nenhuma
mantém, nesta amostra, DTO tipado de request repetindo as mesmas validações.
O probe duplica oito campos apenas para testar equivalência. O mapeamento entre
`declarative.Field` e `rest.Field` é manutenção do framework, não repetição de
DTOs observada em consumidores.

Não há histórico amostrado de alterações, tempo medido de edição ou vários
consumidores independentes repetindo DTOs. Contagem de campos não mede percentual
de produtividade. Revisitar geração quando um consumidor fornecer alterações
recorrentes, artefatos duplicados e direção autoral cuja economia possa ser medida.
Não gerar YAML e Go como duas fontes editáveis.

## Verificação e próximo incremento

Passaram os testes existentes focados, com Go 1.27.2:

```sh
go test ./declarative ./rest -run 'Test(ParseAndBuild|NestedSchemaAndItems|FileBuild_|Swagger)' -count=1
```

O ensaio externo concluiu 21 casos com SQLite real e contratos gerados. Não foram
adicionados testes permanentes nem código de produção. O próximo incremento deve
corrigir C6-01 com regressões relevantes, documentar C6-02–04 e repetir o estudo
antes de declarar a comparação validada. Aceitar ADR-0007 e identificar o primeiro
consumidor real de ferramentas continuam sendo decisões separadas.

## Correção posterior de C6-01 — 2026-11-16

O exportador agora constrói o schema de PATCH com as regras completas e remove
somente `required` do primeiro nível. Isso preserva obrigatoriedade em objetos
aninhados e itens de listas. O validador de runtime e os schemas de resposta não
mudaram. Os resultados acima registram a medição original, anterior à correção.

O teste permanente `TestSwaggerPatchNestedRequiredMatchesValidation` compara o
componente real publicado em OpenAPI com `ValidateDictPartial` em 12 casos,
incluindo profundidade adicional, listas aninhadas, null, defaults e omissão no
primeiro nível. Antes da correção, quatro subcasos falharam; depois, passaram.
A repetição dos 21 casos confirmou a rejeição no schema dos dois casos C6-01,
alinhada ao HTTP 422. Restaram exatamente quatro diferenças esperadas: unicidade,
dois callbacks e exclusão de campo obrigatório nullable por merge patch
(C6-02–04). Passou também `go test ./rest ./declarative ./restsqlite -count=1`.
As suites que exigem variáveis de conexão externas podem pular seus casos;
não se afirma execução das integrações Mongo/Postgres.

Aceitação do ADR e decisão sobre consumidor continuam abertas; corrigir o
exportador não implementa codegen, catálogo ou MCP.

## Complemento de C6-04 — 2026-11-16

O exportador agora sinaliza `Resource.Validate` com
`x-hyrule-custom-validation: true` no nível do objeto em Input e Patch.
`Field.Check` continua sinalizado no nível da propriedade. Componentes de
resposta não ganham o marcador de recurso. Os testes cobrem ausência de callbacks,
callback de campo, callback de recurso e ambos, verificando também que geração
e validação estrutural não executam callbacks.

Isso torna o contrato mais explícito e não elimina as quatro diferenças esperadas
do ensaio: o servidor continua sendo autoridade para unicidade, callbacks e
remoção de obrigatórios por merge patch. C6-02–03 estão documentados como limites;
C6-05 permanece uma sugestão de DX, sem mudança no parser YAML nesta rodada.
Codegen continua adiado; a aceitação do ADR e o consumidor de IA continuam abertos.

## Ensaio reproduzível

A partir da raiz do repositório, salvar o bloco Go abaixo em
`/tmp/hyrule-phase6-study/main.go` (criar o diretório antes). Extrair a definição
Go original e executar ambos os arquivos com as dependências fixadas do módulo:

```sh
mkdir -p /tmp/hyrule-phase6-study
python3 - <<'EXTRACT'
from pathlib import Path
s = Path('examples/blog-apis/services/comments-api/comments.go').read_text()
a = s.index('var commentsSchema = rest.Schema{')
b = s.index('\n}', a) + 2
Path('/tmp/hyrule-phase6-study/comments.go').write_text(
    'package main\nimport "github.com/Gui-Rondon/hyrule/rest"\n' + s[a:b] + '\n')
EXTRACT
go run /tmp/hyrule-phase6-study/main.go /tmp/hyrule-phase6-study/comments.go
```

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"

	"github.com/Gui-Rondon/hyrule/declarative"
	"github.com/Gui-Rondon/hyrule/platform/sqlite"
	"github.com/Gui-Rondon/hyrule/rest"
	"github.com/Gui-Rondon/hyrule/restsqlite"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type result struct {
	Resource, Case, Method string
	Payload                any
	SchemaAccept           bool
	HTTP                   int
	Body                   any
}

var results []result

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func decode(s string) map[string]any {
	var m map[string]any
	must(json.Unmarshal([]byte(s), &m))
	return m
}
func request(api *rest.API, method, path, payload, etag string) (int, map[string]any) {
	r := httptest.NewRequest(method, path, strings.NewReader(payload))
	r.Header.Set("Content-Type", "application/merge-patch+json")
	if etag != "" {
		r.Header.Set("If-Match", `"`+etag+`"`)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	var out map[string]any
	must(json.Unmarshal(w.Body.Bytes(), &out))
	return w.Code, out
}
func schemaFor(doc map[string]any, resource, kind string) *jsonschema.Schema {
	paths := doc["paths"].(map[string]any)
	op := paths["/"+resource].(map[string]any)["post"].(map[string]any)
	ref := op["requestBody"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["oneOf"].([]any)[0].(map[string]any)["$ref"].(string)
	ref = strings.TrimSuffix(ref, "Input") + kind
	c := jsonschema.NewCompiler()
	must(c.AddResource("urn:study", doc))
	s, err := c.Compile("urn:study" + ref)
	must(err)
	return s
}
func run(res *rest.Resource, cases []struct{ name, method, body string }) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, ":memory:")
	must(err)
	defer db.Close()
	adapter, err := restsqlite.NewAdapter(ctx, db, res.Name, res)
	must(err)
	res.Adapter = adapter
	res.ItemMethods = []string{"GET", "PATCH", "PUT"}
	res.RequireMergePatchContentType = true
	api := rest.New(rest.Options{Transactor: sqlite.Transactor{DB: db}})
	api.Register(res)
	_, doc := request(api, "GET", "/api/openapi.json", "", "")
	b, _ := json.MarshalIndent(doc, "", "  ")
	must(os.WriteFile("/tmp/hyrule-phase6-study/"+res.Name+"-openapi.json", b, 0600))
	input, patch := schemaFor(doc, res.Name, "Input"), schemaFor(doc, res.Name, "Patch")
	var id, etag string
	for _, tc := range cases {
		path := "/" + res.Name
		s := input
		if tc.method == "PATCH" {
			path += "/" + id
			s = patch
		}
		payload := decode(tc.body)
		accepted := s.Validate(payload) == nil
		code, body := request(api, tc.method, path, tc.body, etag)
		results = append(results, result{res.Name, tc.name, tc.method, payload, accepted, code, body})
		if code == 201 || code == 200 {
			if v, ok := body["_id"].(string); ok {
				id = v
			}
			if v, ok := body["_etag"].(string); ok {
				etag = v
			}
		}
		if code == 201 || code == 200 {
			status, saved := request(api, "GET", "/"+res.Name+"/"+id, "", "")
			if status != 200 {
				panic("readback failed")
			}
			results = append(results, result{Resource: res.Name, Case: tc.name + " persisted", Method: "GET", HTTP: 200, Body: saved})
		}
	}
}

const extendedYAML = `resources:
- name: probe
  collection: probe
  resource_methods: [get, post]
  item_methods: [get, patch, put]
  schema:
    name: {type: string, required: true, unique: true}
    enabled: {type: boolean, default: true}
    optional: {type: string, nullable: true}
    required_null: {type: string, required: true, nullable: true}
    profile:
      type: dict
      schema:
        label: {type: string, required: true}
        active: {type: boolean, default: true}
    rows:
      type: list
      items:
        type: dict
        schema:
          label: {type: string, required: true}
    labels:
      type: list
      items: {type: string}
    checked: {type: string}
`

func main() {
	// Verify that the separately compiled Go definition was extracted unchanged.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "examples/blog-apis/services/comments-api/comments.go", nil, 0)
	must(err)
	var actual string
	ast.Inspect(f, func(n ast.Node) bool {
		if v, ok := n.(*ast.ValueSpec); ok && len(v.Names) == 1 && v.Names[0].Name == "commentsSchema" {
			var b bytes.Buffer
			must(format.Node(&b, fset, v.Values[0]))
			actual = b.String()
		}
		return true
	})
	must(os.WriteFile("/tmp/hyrule-phase6-study/comments-schema-source.txt", []byte(actual), 0600))
	fmt.Println("Go comments schema fields:", len(commentsSchema))
	data, err := os.ReadFile("examples/standalone-tags-api/resources.yaml")
	must(err)
	file, err := declarative.Parse(bytes.NewReader(data))
	must(err)
	tags, err := file.Resources[0].Build(nil)
	must(err)
	run(tags, []struct{ name, method, body string }{
		{"required missing", "POST", `{}`}, {"required null", "POST", `{"name":null,"slug":"x"}`},
		{"default applied", "POST", `{"name":"First","slug":"first"}`},
		{"unknown field", "POST", `{"name":"Other","slug":"other","extra":true}`},
		{"duplicate unique", "POST", `{"name":"Duplicate","slug":"first"}`},
		{"patch omit required", "PATCH", `{"enabled":false}`},
		{"patch remove required", "PATCH", `{"name":null}`},
	})
	run(&rest.Resource{Name: "comments", Schema: commentsSchema, ResourceMethods: []string{"GET", "POST"}}, []struct{ name, method, body string }{
		{"required missing", "POST", `{}`}, {"default applied", "POST", `{"post_id":"p","author_name":"A","body":"hello"}`},
		{"status unconstrained", "PATCH", `{"status":"not-a-real-status"}`},
		{"patch omit required", "PATCH", `{"body":"edited"}`},
	})
	file, err = declarative.Parse(strings.NewReader(extendedYAML))
	must(err)
	probe, err := file.Resources[0].Build(nil)
	must(err)
	reference := rest.Schema{
		"name": {Type: rest.TypeString, Required: true, Unique: true}, "enabled": {Type: rest.TypeBoolean, Default: true},
		"optional": {Type: rest.TypeString, Nullable: true}, "required_null": {Type: rest.TypeString, Required: true, Nullable: true},
		"profile": {Type: rest.TypeDict, Schema: rest.Schema{"label": {Type: rest.TypeString, Required: true}, "active": {Type: rest.TypeBoolean, Default: true}}},
		"rows":    {Type: rest.TypeList, Items: &rest.Field{Type: rest.TypeDict, Schema: rest.Schema{"label": {Type: rest.TypeString, Required: true}}}},
		"labels":  {Type: rest.TypeList, Items: &rest.Field{Type: rest.TypeString}}, "checked": {Type: rest.TypeString},
	}
	equal := reflect.DeepEqual(probe.Schema, reference)
	fmt.Println("Extended YAML/Go runtime schemas equal:", equal)
	if !equal {
		panic("schema mismatch")
	}
	field := probe.Schema["checked"]
	field.Check = func(_ context.Context, v any, _ map[string]any) any {
		if v == "deny" {
			return "blocked by Field.Check"
		}
		return nil
	}
	probe.Schema["checked"] = field
	probe.Validate = func(_ context.Context, doc, original rest.Doc, partial bool) (rest.Issues, error) {
		if doc["checked"] == "resource-deny" {
			return rest.Issues{"checked": "blocked by Resource.Validate"}, nil
		}
		return nil, nil
	}
	run(probe, []struct{ name, method, body string }{
		{"nullable create", "POST", `{"name":"one","required_null":null,"optional":null,"profile":{"label":"L"},"rows":[{"label":"R"}],"labels":["a"]}`},
		{"list wrong item", "PATCH", `{"labels":[12]}`},
		{"nested required omitted", "PATCH", `{"profile":{}}`},
		{"list object required omitted", "PATCH", `{"rows":[{}]}`},
		{"field callback", "PATCH", `{"checked":"deny"}`},
		{"resource callback", "PATCH", `{"checked":"resource-deny"}`},
		{"required nullable deletion", "PATCH", `{"required_null":null}`},
		{"optional null deletion", "PATCH", `{"optional":null}`},
		{"nullable create omitted required", "POST", `{"name":"two"}`},
		{"nested default", "POST", `{"name":"two","required_null":"v","profile":{"label":"L"}}`},
	})
	output, err := json.MarshalIndent(results, "", "  ")
	must(err)
	must(os.WriteFile("/tmp/hyrule-phase6-study/results.json", output, 0600))
	for _, r := range results {
		if r.Method != "GET" {
			fmt.Printf("%s | %s | schema=%t | HTTP=%d\n", r.Resource, r.Case, r.SchemaAccept, r.HTTP)
		}
	}
}
```
