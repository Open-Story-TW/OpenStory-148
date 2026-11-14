"""Export immutable Commodity WZ facts without opening or changing a server DB."""
import argparse
import hashlib
import json
from pathlib import Path
import xml.etree.ElementTree as ET

def main():
    parser=argparse.ArgumentParser();parser.add_argument('source',type=Path);args=parser.parse_args()
    raw=args.source.read_bytes();root=ET.fromstring(raw);items=[];invalid=[]
    for node in root:
        data={child.get('name'):child.get('value') for child in node}
        sn=int(data.get('SN',0))
        if sn>0:
            try:
                items.append([sn,int(data.get('ItemId',0)),int(data.get('Count',1)),int(data.get('Price',0)),int(data.get('Period',0)),int(data.get('Gender',2)),int(data.get('OnSale',0)),int(data.get('Limit',0))])
            except (TypeError,ValueError):
                invalid.append({'sn':sn,'reason':'non-integer source field','source':data})
    out={'version':2,'source_sha256':hashlib.sha256(raw).hexdigest(),'items':sorted(items),'invalid':invalid}
    Path(__file__).with_name('cash_catalog.json').write_text(json.dumps(out,separators=(',',':'))+'\n',encoding='utf-8')
    print(json.dumps({'items':len(items),'invalid':len(invalid),'on_sale':sum(bool(x[6]) and x[3]>0 for x in items),'source_sha256':out['source_sha256']}))
if __name__=='__main__':main()
