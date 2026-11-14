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
