"""Read-only WZ XML -> deterministic embedded Maple Island fixture and hashes."""
import hashlib, json, pathlib, sys, xml.etree.ElementTree as ET

root=pathlib.Path(sys.argv[1]).resolve()
out=pathlib.Path(__file__).with_name("starter.json")
sources={}
def read(path):
    data=path.read_bytes(); sources[path.relative_to(root).as_posix()]=hashlib.sha256(data).hexdigest()
    return ET.fromstring(data)
def vals(node):
    return {x.get("name"):x.get("value") for x in ([] if node is None else node) if x.tag!="imgdir"}
def num(v,k,d=0): return int(v.get(k,d))
maps=[]; mobs={}
npcnames=vals(None)
for p in sorted((root/"Map.wz"/"Map"/"Map0").glob("*.img.xml")):
    mid=int(p.name.split(".")[0])
    if mid>2000000: continue
    tree=read(p); info=vals(tree.find('./imgdir[@name="info"]'))
    portals=[];life=[];footholds=[]
    for x in tree.find('./imgdir[@name="portal"]') or []:
        v=vals(x); portals.append(dict(id=int(x.get('name')),name=v.get('pn',''),type=num(v,'pt'),x=num(v,'x'),y=num(v,'y'),target_map=num(v,'tm',999999999),target_name=v.get('tn',''),script=v.get('script','')))
    if not any(p['type']==0 for p in portals):continue
    for x in tree.find('./imgdir[@name="life"]') or []:
        v=vals(x); tid=num(v,'id'); kind=v.get('type','')
        life.append(dict(index=int(x.get('name')),kind=kind,template_id=tid,x=num(v,'x'),y=num(v,'cy',num(v,'y')),foothold=num(v,'fh'),facing=num(v,'f'),rx0=num(v,'rx0'),rx1=num(v,'rx1'),hidden=num(v,'hide')!=0,respawn_seconds=num(v,'mobTime')))
        mp=root/'Mob.wz'/f'{tid:07d}.img.xml'
        if kind=='m' and tid not in mobs and mp.exists():
            mt=read(mp); mi=vals(mt.find('./imgdir[@name="info"]'))
            # info/link selects shared animation resources. Each monster's
            # HP, EXP, level and combat flags still come from its own info.
            mobs[tid]=dict(id=tid,level=num(mi,'level',1),hp=num(mi,'maxHP'),mp=num(mi,'maxMP'),exp=num(mi,'exp'),pad=num(mi,'PADamage'),pdd=num(mi,'PDDamage'),boss=num(mi,'boss')!=0,pd_rate=num(mi,'PDRate'),only_normal_attack=num(mi,'onlyNormalAttack')!=0,invincible=num(mi,'invincible')!=0,fixed_damage=num(mi,'fixedDamage'),has_fixed_damage='fixedDamage' in mi)
    for x in tree.findall('./imgdir[@name="foothold"]/imgdir/imgdir/imgdir'):
        v=vals(x); footholds.append(dict(id=int(x.get('name')),x1=num(v,'x1'),y1=num(v,'y1'),x2=num(v,'x2'),y2=num(v,'y2')))
    maps.append(dict(id=mid,return_map=num(info,'returnMap',mid),field_limit=num(info,'fieldLimit'),town=num(info,'town')!=0,portals=portals,life=life,footholds=footholds,**({'fly':True} if num(info,'fly') else {})))
out.write_text(json.dumps(dict(version=1,maps=maps,mobs=list(mobs.values()),sources=sources),ensure_ascii=False,sort_keys=True,indent=2)+'\n',encoding='utf8')
print(f'{len(maps)} maps, {len(mobs)} mob templates, {len(sources)} source hashes -> {out}')
