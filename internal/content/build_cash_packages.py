"""Export read-only Legacy CashPackage WZ composition for server pricing."""
import hashlib
import json
from pathlib import Path
import sys
import xml.etree.ElementTree as ET

raw = Path(sys.argv[1]).read_bytes()
packages = {}
for node in ET.fromstring(raw):
    serials = node.find("imgdir[@name='SN']")
    if serials is not None:
        packages[node.attrib['name']] = [int(v.attrib['value']) for v in serials]
out = {'source_sha256': hashlib.sha256(raw).hexdigest(), 'packages': packages}
Path(__file__).with_name('cash_packages.json').write_text(json.dumps(out, separators=(',', ':'))+'\n', encoding='utf-8')
print(json.dumps({'packages': len(packages), 'source_sha256': out['source_sha256']}))
