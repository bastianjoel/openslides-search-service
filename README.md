# openslides-search-service

The OpenSlides search service.

## Configuration:


| Env variable                    | Default value              | Meaning |
| ------------------------------- | -------------------------- | ------- |
| `SEARCH_PORT`                   | `9050`                     | Port the service listens on.    |
| `SEARCH_HOST`                   | ``                         | Host the service is bound to.   |
| `SEARCH_MAX_QUEUED`             | `5`                        | Number of waiting queries.      |
| `SEARCH_INDEX_AGE`              | `100ms`                    | Accepted age of internal index. |
| `SEARCH_INDEX_FILE`             | `search.bleve`             | Filename of the internal index. |
| `SEARCH_INDEX_BATCH`            | `4096`                     | Batch size of the index when its build or re-generated. |
| `SEARCH_INDEX_UPDATE_INTERVAL`  | `120s`                     | Poll intervall to update the index without queries. |
| `MODELS_YML_FILE`               | `models.yml`               | File path of the used models. |
| `SEARCH_YML_FILE`               | `search.yml`               | Fields of the models to be searched. |
| `DATABASE_NAME`                 | `openslides`               | Name of the database. |
| `DATABASE_USER`                 | `openslides`               | Database user. |
| `DATABASE_HOST`                 | `localhost`                | Host of the database. |
| `DATABASE_PORT`                 | `5432`                     | Port of the database. |
| `DATABASE_PASSWORD_FILE`        | `/run/secrets/postgres_password` | Password file of the database user. |
| `RESTRICTER_URL`                | ``                         | URL to use the restricter from the auto-update-service to filter the query results.|
