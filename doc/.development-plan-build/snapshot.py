from pathlib import Path
import urllib.request, json, datetime, hashlib
stage=Path('runtime/doc-build/development-plan-build')
for name in ['exchangeInfo','fundingInfo']:
    url='https://fapi.binance.com/fapi/v1/'+name
    try:
        data=urllib.request.urlopen(url,timeout=20).read()
        stamp=datetime.datetime.now(datetime.timezone.utc).isoformat()
        (stage/(name+'.json')).write_bytes(data)
        info=json.loads(data)
        rows=info['symbols'] if name=='exchangeInfo' else info
        target=next(x for x in rows if x['symbol']=='SOXLUSDT')
        (stage/(name+'-evidence.json')).write_text(json.dumps({'url':url,'retrieved_at_utc':stamp,'sha256':hashlib.sha256(data).hexdigest(),'symbol_record':target},ensure_ascii=False,indent=2),encoding='utf-8')
        print(name,stamp,'saved')
    except Exception as exc: print(name,'snapshot failed:',str(exc))
