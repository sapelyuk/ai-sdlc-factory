# JSON-Schema файлы для Registry

Этот каталог содержит JSON-Schema файлы (Draft 2020-12) для валидации спецификаций Registry.

## Файлы

| Файл | Описание |
|---|---|
| `role.schema.json` | Спецификация Role (шаблон агента) |
| `team.schema.json` | Спецификация Team (набор агентов) |
| `process.schema.json` | Спецификация Process (workflow SDLC) |

## Валидация

Все схемы используют `additionalProperties: false` для строгой валидации.
Обязательные поля указаны в массиве `required`.

Использование в Go:
```go
import "github.com/santhosh-tekuri/jsonschema/v5"

schema, _ := jsonschema.CompileString("file://packages/contracts/schemas/role.schema.json")
if err := schema.Validate(data); err != nil {
    // validation error
}
```
