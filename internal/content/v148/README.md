# v148 supplement

Client data the v145 data root lacks, written by `cmd/wzexport` from the v148
client and laid out like the data root. `openWZ` serves a file from here only
when the root has no file of that path.

| Path | Kept | Used for |
|---|---|---|
| `Map.wz/Map/Map9/9150000xx`, `9150100xx`, `9150101xx`, `9150102xx` | info, life, foothold, ladderRope, portal, reactor | Phantom tutorial and job advancement maps |
| `Map.wz/Map/Map1/1500xxxxx` | same | 水晶花園, the Phantom home ship |
| `Character.wz` 01050222, 01051272, 01102347, 01070023, 01071035, 01003421, 013521xx | info | Phantom starter outfit and card decks |
| `Mob.wz` 9300498, 9300507 | all | guards of the tutorial |

Regenerate a file with, for example:

    go run ./cmd/wzexport -client <v148 client folder> -out internal/content/v148 -keep info,life,foothold,ladderRope,portal,reactor Map.wz/Map/Map9/915000000.img

## Skill books

`Skill.wz/<job>.img.xml` holds every job's skill book of the v148 client (the
`skill` entry only). Unlike the files above these are read first: where the data
root has the unchanged v145 book (hashes in `../v145_skill_books.json`) or no
book, `SkillDefinition` uses the v148 one, and falls back to the v145 file only
for a skill v148 dropped. A book the operator changed is the only one read.

    go run ./cmd/wzexport -client <v148 client folder> -out internal/content/v148 -keep skill "Skill.wz/[0-9]*.img"

Books 9000 and up (guild, professions) are not kept here; delete them after an
export.
