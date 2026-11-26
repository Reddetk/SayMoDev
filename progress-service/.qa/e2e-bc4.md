# BC#4 progress-service — E2E report

- Date: 2026-11-25
- Base URL: `http://127.0.0.1:8082` (catalog stub `http://127.0.0.1:8083`)
- Method: black-box HTTP only (no source reading). Tokens minted with `cmd/tokenmint`, one fresh account UUID per scenario (`11111111-1111-4111-8111-00000000000N`).
- All program/lesson IDs were taken from API responses; program `db5f5b48-7fc5-5b9c-ae3b-77085fab1954` (easy, v1, 3 lessons: 2 regular + 1 creative) came from `GET /progress/me/activate`.

## Results

| # | scenario | request | expected | actual | PASS/FAIL |
|---|---|---|---|---|---|
| 1 | 1. new patient | `GET /progress/me` | 404 | 404 `no_active_program` | PASS |
| 2 | 1. activate by classifier | `POST /progress/me/activate` `{classifier:{easy,motor_aphasia}}` | 201, completionPercent 0, totalLessons, accessibleLesson{lessonId,orderPosition} | 201, all fields, orderPosition 1 | PASS |
| 3 | 1. read after activate | `GET /progress/me` | 200, status active, completionPercent 0, first accessible lesson | 200, status active, 0/3, accessibleLesson regular, unlockReason immediate, nextUnlockDate null | PASS |
| 4 | 1. idempotent replay | same `Idempotency-Key`, same body | identical stored 201 | 201, byte-identical payload | PASS |
| 5 | 1. second activation | new `Idempotency-Key` | 409 `program_already_active` | 409 `program_already_active` | PASS |
| 6 | 2. answer #1 | `POST /lessons/{id}/answers` text | 201 {answerId,submittedAt,message} | 201 | PASS |
| 7 | 2. answer #2 | same lesson, text | 201 | 201 | PASS |
| 8 | 2. admin list | `GET /admin/answers/{acct}` | total=2, newest first | total=2, `second answer` before `first answer` | PASS |
| 9 | 2. admin default paging | `GET /admin/answers/{acct}` | limit 50 | limit 50, offset 0 | PASS |
| 10 | 2. admin max paging | `?limit=999` | clamp to 200 | limit 200 | PASS |
| 11 | 2. admin by lesson | `GET /admin/answers/{acct}/{lessonId}` | 200 [], submittedAt ASC | 200 [], ascending | PASS |
| 12 | 3. complete regular | `POST /lessons/{id}/complete` | 200, percent>0, nextLessonId non-null | 200, 33, next lesson id | PASS |
| 13 | 3. replay same key | same `Idempotency-Key` | same 200 | 200, identical body | PASS |
| 14 | 3. new key on completed lesson | new key | 409 `lesson_already_completed` | 409 `lesson_already_completed` | PASS |
| 15 | 3. answer after completion | `POST answers` | 409 `lesson_already_completed` | 409 `lesson_already_completed` | PASS |
| 16 | 3. read after completion | `GET /progress/me` | next lesson accessible, completedLessons 1 | 200, 1/3, 33, next regular lesson | PASS |
| 17 | 4. bad photo payload | `{mediaType:photo,payload:"http://evil/x.jpg"}` | 422 | 422 `invalid_payload` | PASS |
| 18 | 4. bogus mediaType | `{mediaType:"bogus"}` | 422 | 422 `invalid_media_type` | PASS |
| 19 | 4. missing Idempotency-Key | `POST answers` without header | 400/409 | 400 `idempotency_key_required` | PASS |
| 20 | 4. bad lesson UUID (complete) | `/lessons/not-a-uuid/complete` | 400 | 400 `invalid_path_param` | PASS |
| 21 | 4. bad lesson UUID (answers) | `/lessons/not-a-uuid/answers` | 400 | 400 `invalid_path_param` | PASS |
| 22 | 5. final completion | complete creative lesson (no answer) | 200, percent=100, nextLessonId=null | 200, 100, null | PASS |
| 23 | 5. completed read | `GET /progress/me` | status completed, accessibleLesson null | completed, 3/3, 100, accessibleLesson null | PASS |
| 24 | 5. admin view | `GET /admin/progress/{acct}` | completed | status completed, completedAt set, pausedPrograms [] | PASS |
| 25 | 6. switch difficulty | `PATCH /progress/difficulty` medium | 200 {old,new,old%,new%=0,message} | 200, oldCompletionPercent 0, newCompletionPercent 0 | PASS |
| 26 | 6. read after switch | `GET /progress/me` | programId changed, completionPercent 0 | new medium program id, 0/3 | PASS |
| 27 | 6. admin paused programs | `GET /admin/progress/{acct}` | old program in pausedPrograms | pausedPrograms[0].programId = old easy id, status paused | PASS |
| 28 | 6. complete medium program | loop answers+complete | 100% | 100% | PASS |
| 29 | 6. resume easy | `POST /programs/{oldEasyId}/resume` | 200 {programId,status,completionPercent,accessibleLesson,message} | 200, status active, 0%, lesson 1 | PASS |
| 30 | 6. read after resume | `GET /progress/me` | easy active, frozen progress | easy active, 0/3 | PASS |
| 31 | 6. frozen progress (non-zero variant) | complete 1 easy lesson (33%) → switch hard → complete hard → resume easy | 200, frozen 33% restored | 200, completionPercent 33, completedLessons 1, accessibleLesson orderPosition 2 | PASS |
| 32 | 6. same difficulty | `PATCH {newDifficulty:"easy"}` while on easy | 400 | 400 `same_difficulty` | PASS |
| 33 | 6. invalid difficulty | `PATCH {newDifficulty:"expert"}` | 400 | 400 `invalid_difficulty` | PASS |
| 34 | 7. presign without S3 | `POST /lessons/{id}/answers/presign` {photo,image/jpeg} | 503 `s3_not_configured` | 503 `s3_not_configured` | PASS |
| 35 | 7. presign text | presign {mediaType:text} | 422 | 422 `invalid_media_type` | PASS |
| 36 | 8. admin route, patient token | `GET /admin/progress/{acct}` | 403 | 403 `forbidden` | PASS |
| 37 | 8. admin route, admin token | same | 200 | 200, full progress payload | PASS |
| 38 | 8. admin route, no token | same | 401 | 401 `invalid_token` | PASS |
| 39a | 9. deletion guard, no token | `GET /admin/progress/program/{programId}/version/1/active-patients` | 401 `invalid_token` (new contract: `X-Service-Token` required) | 401 `invalid_token` | PASS |
| 39b | 9. deletion guard, service token | same + `X-Service-Token: local-service-token` | 200 {programId,version,activePatientCount,patientIds} | 200, programId/version=1/count=10/10 patientIds | PASS |
| 39c | 9. deletion guard, wrong token | same + `X-Service-Token: wrong-token` | 401 `invalid_token` | 401 `invalid_token` | PASS |
| 40 | 10. GDPR no header | `DELETE /progress/me` | 400 `confirmation_required` | 400 `confirmation_required` | PASS |
| 41 | 10. GDPR wrong header value | `X-Confirmation: yes` | 400 `confirmation_required` | 400 `confirmation_required`, data intact | PASS |
| 42 | 10. GDPR delete | `X-Confirmation: delete-my-progress` | 204 | 204, empty body | PASS |
| 43 | 10. read after delete | `GET /progress/me` | 404 | 404 `no_active_program` | PASS |
| 44 | 10. admin answers after delete | `GET /admin/answers/{acct}` | total>=1, payload anonymised | total=1, payload `[deleted by user]` | PASS |
| 45 | 11. garbage token | `GET /progress/me` Bearer garbage | 401 | 401 `invalid_token` | PASS |
| 46 | 11. expired token | Bearer token `-ttl -2m` | 401 | 401 `invalid_token` | PASS |
| 47 | extra: activate by programId | `POST /progress/me/activate {programId}` | 201 | 201, same contract shape | PASS |
| 48 | extra: complete regular w/o answer | `POST /lessons/{id}/complete` | 422 `no_answer_submitted` | 422 `no_answer_submitted` | PASS |
| 49 | extra: complete out-of-order lesson | complete lesson 2 while lesson 1 accessible | 403 `lesson_not_accessible` | 403 `lesson_not_accessible` | PASS |
| 50 | extra: resume active program | resume currently active program | 404 `program_not_paused` | 404 `program_not_paused` | PASS |
| 51 | extra: resume unknown program | resume non-existent program UUID (current program completed, guard order satisfied) | 404 `program_no_longer_exists` | 404 `program_no_longer_exists` (re-run after fix; originally `program_not_paused`) | PASS |
| 52 | extra: admin difficulty change | `PATCH /admin/progress/{acct}/difficulty` {hard} | 200 | 200, old/new ids + message | PASS |
| 53 | extra: unknown classifier | activate {easy, nonexistent_aphasia} | 404 `no_program_for_classifier` | 404 `no_program_for_classifier` | PASS |

## Summary

- Checks: 55 (row 39 split into 39a–39c after the service-token change)
- PASS: 55
- FAIL: 0
- Scenarios 1–11 all pass. The former F1 failure was fixed by the server update and re-verified on 2026-11-25 20:09:57Z (23:09:57 +03:00). Scenario 9 re-verified against the new `X-Service-Token` contract on 2026-11-25 20:24:14Z (23:24:14 +03:00).

## Failures (minimal repro)

None outstanding.

### F1 — RESOLVED: `POST /progress/programs/{programId}/resume` with a non-existent programId returned `program_not_paused`

Original repro (2026-11-25 ~20:05Z): account `11111111-1111-4111-8111-000000000005` with its program completed → `POST /progress/programs/00000000-0000-4000-8000-000000000000/resume` → `404 {"error":"program_not_paused"}`, expected `404 {"error":"program_no_longer_exists"}`.

Re-run after fix (2026-11-25 20:09:57Z / 23:09:57 +03:00):

```
precondition: GET /progress/me -> 200 status=completed, completedLessons=3, completionPercent=100
resume unknown           -> 404 {"error":"program_no_longer_exists"}   PASS
resume existing non-paused (db5f5b48-7fc5-5b9c-ae3b-77085fab1954)
                         -> 404 {"error":"program_not_paused"}          PASS
```

## Re-run after fix

- Timestamp: 2026-11-25 20:09:57Z (23:09:57 +03:00).
- Server restarted with updated build; same patient account (`…0005`, program completed) reused.
- Only the previously failing resume case and its neighbour (existing but not-paused program) were re-executed; all other rows retain their original results.
- Both responses now match the contract exactly, so row 51 flips to PASS and the totals become 53/53.

## Re-run after fix — scenario 9, service-token guard

- Timestamp: 2026-11-25 20:24:14Z (23:24:14 +03:00).
- Contract change: the guard now requires `X-Service-Token: local-service-token` and no longer accepts unauthenticated calls.
- same endpoint: `GET /admin/progress/program/db5f5b48-7fc5-5b9c-ae3b-77085fab1954/version/1/active-patients`

```
no header                 -> 401 {"error":"invalid_token"}          PASS (was 200 before the change)
X-Service-Token: local-service-token
                          -> 200 {programId:"db5f5b48-…1954", version:1, activePatientCount:10, patientIds:[…10 ids]}  PASS
X-Service-Token: wrong-token
                          -> 401 {"error":"invalid_token"}          PASS
```

- `patientIds` now also contains two accounts created after the first pass (`de5a6b06-…`, `7c6d9a30-…`), hence `activePatientCount` 10 instead of 8; all our test accounts with active programs are present, completed/deleted ones are not.
- Row 39 was split into 39a–39c (3 checks), so totals are 55 checks / 55 PASS / 0 FAIL.


## Observations / notes (not counted as failures)

1. `completionPercent` is integer-truncated per lesson step: 1/3 → `33`, 2/3 → `66`. At program end it reaches exactly `100`. The contract does not state rounding.
2. `tariffSnapshot.weekBitmap` is returned as a number (`127`), not an array; the contract does not fix its type. `validUntil` and `nextUnlockDate` are `null` in this environment.
3. `GET /progress/me` on a non-existent/paused program returns `404 {"error":"no_active_program"}`; contract prose only says "404 no active program".
4. Deletion guard returned seeded/pre-existing account IDs (`11111111-1111-1111-1111-111111111111`, `33333333-3333-3333-3333-333333333333`) in `patientIds` in addition to test accounts. Count semantics (completed/deleted accounts excluded) behaved correctly.
5. The catalog stub exposes no photo-typed lesson, so scenario 7 presign was exercised against the currently accessible (regular) lesson; behavior (503 `s3_not_configured` / 422 for `text`) matched expectations.
6. Guard order in resume: while the current program is not completed, any resume attempt returns `400 current_program_not_completed` (unknown target included) — this masked F1 in the first pass and was re-tested after completing the current program.

## Full re-run 2026-11-25 20:52:14Z–20:55:49Z (23:52:14–23:55:49 +03:00)

Fresh build at `http://127.0.0.1:8082` (migrations 15), 14 brand-new accounts `22222222-2222-4222-8222-0000000000NN`, fresh admin token (`88888888-…-0001`). Catalog `programId`/`version`/lesson ids re-fetched from `GET /content/programs`; every other id taken from progress responses. Guard now called with `X-Service-Token`.

| # | scenario | request | expected | actual | PASS/FAIL |
|---|---|---|---|---|---|
| 1 | 1. new patient | `GET /progress/me` | 404 | 404 `no_active_program` | PASS |
| 2 | 1. activate by classifier | `POST /progress/me/activate` | 201, 0%, totalLessons, accessibleLesson{lessonId,orderPosition} | 201, 0%, 3 lessons, orderPosition 1 | PASS |
| 3 | 1. read after activate | `GET /progress/me` | 200 active, 0%, first lesson | 200, 0/3, regular lesson, unlockReason immediate, nextUnlockDate null | PASS |
| 4 | 1. idempotent replay | same key/body | identical stored 201 | 201, identical payload | PASS |
| 5 | 1. second activation | new key | 409 `program_already_active` | 409 `program_already_active` | PASS |
| 6 | 2. activate | `POST /progress/me/activate` | 201 | 201 | PASS |
| 7 | 2. answer #1 | `POST answers` text | 201 | 201 {answerId,submittedAt,message} | PASS |
| 8 | 2. answer #2 | `POST answers` text | 201 | 201 | PASS |
| 9 | 2. admin list | `GET /admin/answers/{acct}` | total=2, newest first | total=2, second before first, limit 50 offset 0 | PASS |
| 10 | 2. admin paging max | `?limit=999` | limit 200 | limit 200 | PASS |
| 11 | 2. admin by lesson | `GET /admin/answers/{acct}/{lessonId}` | 200 [], ASC | 200 [], ASC | PASS |
| 12 | 3. answers/completion setup | activate + answer | 201/201 | 201/201 | PASS |
| 13 | 3. complete regular | `POST /lessons/{id}/complete` | 200, >0%, nextLessonId | 200, 33, next id | PASS |
| 14 | 3. replay same key | same key | same 200 | 200, identical body | PASS |
| 15 | 3. new key | new key | 409 `lesson_already_completed` | 409 | PASS |
| 16 | 3. answer after completion | `POST answers` | 409 | 409 `lesson_already_completed` | PASS |
| 17 | 3. read after completion | `GET /progress/me` | next lesson, 1 completed | 1/3, 33, next regular lesson | PASS |
| 18 | 4. activate | `POST /progress/me/activate` | 201 | 201 | PASS |
| 19 | 4. photo http payload | `{photo,"http://evil/x.jpg"}` | 422 | 422 `invalid_payload` | PASS |
| 20 | 4. bogus mediaType | `{mediaType:"bogus"}` | 422 | 422 `invalid_media_type` | PASS |
| 21 | 4. missing Idempotency-Key | answers without header | 400/409 | 400 `idempotency_key_required` | PASS |
| 22 | 4. bad lesson UUID (complete) | `/lessons/not-a-uuid/complete` | 400 | 400 `invalid_path_param` | PASS |
| 23 | 4. bad lesson UUID (answers) | `/lessons/not-a-uuid/answers` | 400 | 400 `invalid_path_param` | PASS |
| 24 | 5. activate | `POST /progress/me/activate` | 201 | 201 | PASS |
| 25 | 5. lesson 1 regular | answer + complete | 200, 33%, next non-null | 200, 33, next id | PASS |
| 26 | 5. lesson 2 regular | answer + complete | 200, 66%, next non-null | 200, 66, next id | PASS |
| 27 | 5. lesson 3 creative | complete without answer | 200, 100%, next null | 200, 100, null | PASS |
| 28 | 5. completed read | `GET /progress/me` | completed, accessibleLesson null | completed, 3/3, 100, null | PASS |
| 29 | 5. admin view | `GET /admin/progress/{acct}` | completed | 100, completedAt set, pausedPrograms [] | PASS |
| 30 | 6. activate easy | `POST /progress/me/activate` | 201 | 201 | PASS |
| 31 | 6. seed frozen progress | complete easy L1 | 200, 33% | 200, 33 | PASS |
| 32 | 6. switch to medium | `PATCH /progress/difficulty` | 200, old 33 / new 0 | 200, oldCompletionPercent 33, newProgramId ≠ old | PASS |
| 33 | 6. read after switch | `GET /progress/me` | new programId, 0% | medium program, 0/3 | PASS |
| 34 | 6. admin paused history | `GET /admin/progress/{acct}` | old program paused | pausedPrograms[0] = old easy id, status paused, completionPercent 33 | PASS |
| 35 | 6. complete medium | loop to end | 100% | completed, 3/3, 100 | PASS |
| 36 | 6. resume easy | `POST /programs/{easy}/resume` | 200, frozen 33% | 200, 33%, accessibleLesson orderPosition 2 | PASS |
| 37 | 6. read after resume | `GET /progress/me` | easy active, 33%, 1 completed | easy, 33, 1/3, next regular lesson | PASS |
| 38 | 6. same difficulty | `PATCH {easy}` while on easy | 400 | 400 `same_difficulty` | PASS |
| 39 | 6. invalid difficulty | `PATCH {expert}` | 400 | 400 `invalid_difficulty` | PASS |
| 40 | 6. missing key | `PATCH` without key | 400/409 | 400 `idempotency_key_required` | PASS |
| 41 | 7. presign without S3 | `POST .../presign` {photo,image/jpeg} | 503 `s3_not_configured` | 503 `s3_not_configured` | PASS |
| 42 | 7. presign text | presign {mediaType:text} | 422 | 422 `invalid_media_type` | PASS |
| 43 | 7. presign gif contentType | {photo,image/gif} | 422 | 422 `invalid_media_type` | PASS |
| 44 | 8. patient token on admin | `GET /admin/progress/{acct}` | 403 | 403 `forbidden` | PASS |
| 45 | 8. admin token | same | 200 | 200 full payload | PASS |
| 46 | 8. no token | same | 401 | 401 `invalid_token` | PASS |
| 47 | 9. guard no token | `GET .../active-patients` | 401 `invalid_token` | 401 `invalid_token` | PASS |
| 48 | 9. guard service token | + `X-Service-Token: local-service-token` | 200 {programId,version,activePatientCount,patientIds} | 200, version 1, count 15, 15 ids | PASS |
| 49 | 9. guard wrong token | + `X-Service-Token: wrong-token` | 401 `invalid_token` | 401 `invalid_token` | PASS |
| 50 | 10. activate + answer | `POST activate`, `POST answers` | 201/201 | 201/201 | PASS |
| 51 | 10. delete no header | `DELETE /progress/me` | 400 `confirmation_required` | 400 | PASS |
| 52 | 10. delete wrong header | `X-Confirmation: yes` | 400 | 400 `confirmation_required` | PASS |
| 53 | 10. delete confirmed | `X-Confirmation: delete-my-progress` | 204 | 204 empty | PASS |
| 54 | 10. read after delete | `GET /progress/me` | 404 | 404 `no_active_program` | PASS |
| 55 | 10. admin answers after delete | `GET /admin/answers/{acct}` | total>=1, anonymised | total=1, payload `[deleted by user]` | PASS |
| 56 | 11. garbage token | `GET /progress/me` | 401 | 401 `invalid_token` | PASS |
| 57 | 11. expired token | `-ttl -2m` token | 401 | 401 `invalid_token` | PASS |
| 58 | x. activate by programId | `POST /progress/me/activate {programId}` | 201 | 201, same contract shape | PASS |
| 59 | x. complete regular w/o answer | `POST /lessons/{id}/complete` | 422 `no_answer_submitted` | 422 | PASS |
| 60 | x. complete out-of-order | complete lesson 2 first | 403 `lesson_not_accessible` | 403 | PASS |
| 61 | x. resume active program | resume while current program incomplete | 400 `current_program_not_completed` | 400 `current_program_not_completed` | PASS |
| 62 | x. complete whole program | loop answers + completes | 100% | 100%, next null, status completed | PASS |
| 63 | x. resume unknown program | resume non-existent UUID (current completed) | 404 `program_no_longer_exists` | 404 `program_no_longer_exists` | PASS |
| 64 | x. resume completed non-paused | resume completed easy program | 404 `program_not_paused` | 404 `program_not_paused` | PASS |
| 65 | x. admin difficulty change | `PATCH /admin/progress/{acct}/difficulty` {medium} | 200 | 200, old/new ids, message | PASS |
| 66 | x. admin read after change | `GET /admin/progress/{acct}` | old program paused | pausedPrograms[0] = old easy id, active = medium | PASS |
| 67 | x. unknown classifier | activate {easy,nonexistent_aphasia} | 404 `no_program_for_classifier` | 404 | PASS |
| 68 | x. activate after 404 | activate easy | 201 | 201 | PASS |

### Full re-run summary

- Checks: 68
- PASS: 68
- FAIL: 0
- Deviations from contract: none.
- All 11 required scenarios re-verified end-to-end on the current build.

### Full re-run notes

1. Deletion guard now enforces `X-Service-Token`: no header and wrong value both 401 `invalid_token`; correct value returns the four documented fields. `activePatientCount` was 15 because parallel/lab accounts with active programs exist in the same database; every test account with an active program was present, completed (acct `…0005`, `…0011`) and deleted (acct `…0010`) accounts were absent.
2. Admin `pausedPrograms` entries now also carry `completionPercent` (frozen at 33 for the paused easy program). This is an addition to the older payload and matches the frozen-progress semantics.
3. Resume error priority is guard-order dependent, as before: with an incomplete current program any resume returns `400 current_program_not_completed`; once the current program is completed, the 404 branches are correct (`program_no_longer_exists` for an unknown target, `program_not_paused` for a real but non-paused one).
4. `completionPercent` remains integer-truncated (33/66) with 100 at program end; `weekBitmap` still numeric (127); `nextUnlockDate`/`validUntil` null.
5. Methodology slip, not a service issue: the admin difficulty change was first sent with `POST` and returned a plain-text `404 page not found`; re-issued with the documented `PATCH`, which returned 200. Row 65 reflects the `PATCH` result.
6. Scenario 7 still uses the regular accessible lesson because the catalog has no photo-typed lesson; 503 `s3_not_configured` and 422 validation behave as specified. An extra `image/gif` contentType probe also returned 422 `invalid_media_type`.

