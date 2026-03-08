package ui

import (
	htmlpkg "html"
	"net/http"
	"strings"
)

// Handler returns an HTTP handler for the Mission Control UI. When apiKey is
// non-empty, a meta tag and JS auth header are injected so the UI can
// authenticate API requests.
func Handler(apiKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := page
		if apiKey != "" {
			meta := `<meta name="beecon-api-key" content="` + htmlpkg.EscapeString(apiKey) + `" />`
			html = strings.Replace(html, "</head>", meta+"\n</head>", 1)
		}
		_, _ = w.Write([]byte(html))
	})
	return mux
}

const page = `<!doctype html>
<html>
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>Beecon Mission Control</title>
<style>
:root {
  --bg:#0f1720; --bg2:#142030; --panel:#101927; --line:#2a3c56;
  --ok:#3ecf8e; --warn:#f7c948; --bad:#ff5d5d; --txt:#e8eff7; --muted:#9bb0c9;
}
body {margin:0; font-family: ui-sans-serif, -apple-system, Segoe UI, sans-serif; background: radial-gradient(circle at 20% -10%, #1f3250, var(--bg)); color:var(--txt);}
header {padding:14px 18px; border-bottom:1px solid var(--line); background:rgba(10,15,24,.5); backdrop-filter: blur(6px);} 
main {display:grid; grid-template-columns: 1fr 1fr 1fr; gap:10px; padding:10px; min-height: calc(100vh - 56px);} 
.panel {background:linear-gradient(180deg, rgba(20,30,45,.95), rgba(15,24,36,.95)); border:1px solid var(--line); border-radius:12px; overflow:hidden;}
.panel h2 {margin:0; padding:10px 12px; font-size:14px; color:var(--muted); border-bottom:1px solid var(--line);} 
.content {padding:10px 12px; height:calc(100vh - 110px); overflow:auto;}
.item {padding:8px; border:1px solid #22344d; border-radius:8px; margin-bottom:8px; background:#0f1a29;}
.small {font-size:12px; color:var(--muted)}
.node {display:flex; justify-content:space-between; gap:10px;}
.badge {padding:2px 6px; border-radius:999px; font-size:11px; border:1px solid}
.b-ok {color:var(--ok); border-color:#2b6f53}
.b-warn {color:var(--warn); border-color:#7a6a2d}
.b-bad {color:var(--bad); border-color:#8a3a3a}
button {background:#1e3050; color:var(--txt); border:1px solid var(--line); border-radius:6px; padding:4px 10px; cursor:pointer; font-size:12px;}
button:hover {background:#2a4060;}
button.btn-ok {border-color:#2b6f53; color:var(--ok);}
button.btn-bad {border-color:#8a3a3a; color:var(--bad);}
.actions {display:flex; gap:6px; margin-top:6px;}
</style>
</head>
<body>
<header><strong>Beecon Mission Control</strong> <span class="small">Intent Feed · Resolution Graph · Audit Rail</span> <button onclick="doApply()" style="margin-left:auto;float:right">Apply</button></header>
<main>
<section class="panel"><h2>Intent Feed</h2><div id="intent" class="content"></div></section>
<section class="panel"><h2>Resolution Graph</h2><div id="graph" class="content"></div></section>
<section class="panel"><h2>Audit Rail</h2><div id="audit" class="content"></div></section>
</main>
<script>
const _bk=document.querySelector('meta[name="beecon-api-key"]');
const _ak=_bk?_bk.content:'';
async function j(url){const o={};if(_ak)o.headers={'Authorization':'Bearer '+_ak};const r=await fetch(url,o);return r.json();}
async function post(url,body){const o={method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)};if(_ak)o.headers['Authorization']='Bearer '+_ak;const r=await fetch(url,o);return r.json();}
function esc(s){return (s??'').toString().replace(/[&<>]/g, c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]));}
function statusBadge(s){
  const t=(s||'').toUpperCase();
  if(t==='APPLIED'||t==='MATCHED') return '<span class="badge b-ok">'+esc(t)+'</span>';
  if(t==='PENDING_APPROVAL'||t==='DRIFTED') return '<span class="badge b-warn">'+esc(t)+'</span>';
  return '<span class="badge b-bad">'+esc(t||'UNKNOWN')+'</span>';
}
async function render(){
  const [runs, approvals, graph, audit] = await Promise.all([
    j('/api/runs').catch(()=>({runs:[]})),
    j('/api/approvals').catch(()=>({approvals:[]})),
    j('/api/graph').catch(()=>({nodes:[],edges:[],actions:[]})),
    j('/api/audit').catch(()=>([])),
  ]);

  const intent = document.getElementById('intent');
  const feed = [];
  for(const r of (runs.runs||[]).slice(0,20)){
    feed.push('<div class="item"><div class="node"><strong>'+esc(r.id)+'</strong>'+statusBadge(r.status)+'</div><div class="small">'+esc(r.beacon_path)+'</div></div>');
  }
  for(const a of (approvals.approvals||[]).slice(0,10)){
    let btns='';
    if((a.status||'').toUpperCase()==='PENDING'){
      btns='<div class="actions"><button class="btn-ok" onclick="doApprove(\''+esc(a.id)+'\')">Approve</button><button class="btn-bad" onclick="doReject(\''+esc(a.id)+'\')">Reject</button></div>';
    }
    feed.push('<div class="item"><div class="node"><strong>approval '+esc(a.id)+'</strong>'+statusBadge(a.status)+'</div><div class="small">'+esc(a.reason)+'</div>'+btns+'</div>');
  }
  intent.innerHTML = feed.join('') || '<div class="small">No runs yet</div>';

  const graphEl = document.getElementById('graph');
  const g = [];
  for(const n of (graph.nodes||[])){
    g.push('<div class="item"><div class="node"><strong>'+esc(n.id)+'</strong><span class="small">'+esc(n.type)+'</span></div></div>');
  }
  for(const e of (graph.edges||[])){
    g.push('<div class="small">'+esc(e.from)+' → '+esc(e.to)+'</div>');
  }
  for(const a of (graph.actions||[])){
    g.push('<div class="item"><div class="node"><strong>'+esc(a.operation)+' '+esc(a.node_id)+'</strong>'+(a.requires_approval?statusBadge('PENDING_APPROVAL'):'')+'</div></div>');
  }
  graphEl.innerHTML = g.join('') || '<div class="small">No graph data</div>';

  const auditEl = document.getElementById('audit');
  const au = [];
  for(const ev of (Array.isArray(audit)?audit:audit.events||[]).slice(-100).reverse()){
    au.push('<div class="item"><div><strong>'+esc(ev.type)+'</strong> <span class="small">'+esc(ev.timestamp)+'</span></div><div class="small">'+esc(ev.message)+'</div></div>');
  }
  auditEl.innerHTML = au.join('') || '<div class="small">No audit events</div>';
}
async function doApply(){
  if(!confirm('Apply infra.beecon?'))return;
  try{const r=await post('/api/apply',{beacon_path:'infra.beecon'});alert('Applied: run '+r.run_id+' ('+r.executed+' executed, '+r.pending+' pending)');}catch(e){alert('Apply failed: '+e);}
  render();
}
async function doApprove(id){
  if(!confirm('Approve '+id+'?'))return;
  try{await post('/api/approve',{request_id:id,approver:'ui-user'});alert('Approved '+id);}catch(e){alert('Approve failed: '+e);}
  render();
}
async function doReject(id){
  const reason=prompt('Rejection reason:','rejected via UI');
  if(reason===null)return;
  try{await post('/api/reject',{request_id:id,approver:'ui-user',reason:reason});alert('Rejected '+id);}catch(e){alert('Reject failed: '+e);}
  render();
}
let _delay=5000,_paused=false;
document.addEventListener('visibilitychange',()=>{_paused=document.hidden;if(!_paused)poll();});
function poll(){if(_paused)return;render().then(()=>{_delay=5000;}).catch(()=>{_delay=Math.min(_delay*2,60000);}).finally(()=>{setTimeout(poll,_delay);});}
poll();
</script>
</body>
</html>`
