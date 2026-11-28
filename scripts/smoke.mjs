import { spawn, execFileSync } from 'node:child_process';
import assert from 'node:assert/strict';
const image='aulaglobal-mcp:local';
const init={jsonrpc:'2.0',id:1,method:'initialize',params:{protocolVersion:'2025-03-26',capabilities:{},clientInfo:{name:'smoke',version:'1'}}};
const list={jsonrpc:'2.0',id:2,method:'tools/list',params:{}};
const auth={jsonrpc:'2.0',id:3,method:'tools/call',params:{name:'auth_status',arguments:{}}};
function checkTools(r) {
 assert.equal(r.result.tools.length,10);
 assert.deepEqual(r.result.tools.map(t=>t.name).sort(),['auth_status','download_files','get_assignment','get_assignment_feedback','list_assignments','list_course_files','list_courses','read_file','search_files','view_image']);
 for(const t of r.result.tools){assert(t.inputSchema);// view_image returns an image, so it has no structured output
  if(t.name==='view_image')assert(!t.outputSchema);else assert(t.outputSchema);}
}
function checkAuth(r){assert.equal(r.result.isError,true);assert.match(JSON.stringify(r),/token missing/);}
const p=spawn('docker',['run','--rm','-i',image,'--transport','stdio'],{stdio:['pipe','pipe','pipe']});
let pending='',stderr='',responses=new Map();
p.stderr.on('data',d=>stderr+=d);
const exited=new Promise(resolve=>p.on('exit',resolve));
p.stdout.on('data',d=>{pending+=d;let i;while((i=pending.indexOf('\n'))>=0){let l=pending.slice(0,i);pending=pending.slice(i+1);if(l){let r=JSON.parse(l);responses.set(r.id,r);}}});
const waitFor=async(id)=>{for(let n=0;n<100;n++){if(responses.has(id))return responses.get(id);await new Promise(r=>setTimeout(r,100));}throw Error('stdio timeout '+stderr);};
try {
 p.stdin.write(JSON.stringify(init)+'\n');assert((await waitFor(1)).result.serverInfo);
 p.stdin.write(JSON.stringify({jsonrpc:'2.0',method:'notifications/initialized'})+'\n');
 p.stdin.write(JSON.stringify(list)+'\n');checkTools(await waitFor(2));
 p.stdin.write(JSON.stringify(auth)+'\n');checkAuth(await waitFor(3));
 console.log('PASS stdio initialize + tools/list (10 tools) + auth_status missing token');
} finally {p.stdin.end();await exited;}
const name='aulaglobal-mcp-smoke-'+process.pid;
execFileSync('docker',['run','-d','--rm','--name',name,'-p','127.0.0.1:18080:8080','-e','MCP_ALLOWED_HOSTS=127.0.0.1:18080','-e','MCP_HTTP_AUTH_TOKEN=smoke-only-not-a-real-secret',image]);
try {
 const post=async(body,headers={})=>{
 const r=await fetch('http://127.0.0.1:18080/mcp',{method:'POST',headers:{'Content-Type':'application/json',Accept:'application/json, text/event-stream',Authorization:'Bearer smoke-only-not-a-real-secret',...headers},body:JSON.stringify(body)});
 const text=await r.text();let value;
 if(r.ok){if(text.startsWith('event:')||text.startsWith('data:')){const line=text.split('\n').find(x=>x.startsWith('data:'));value=JSON.parse(line.slice(5));}else value=JSON.parse(text);}
 return {r,value};
 };
 let ready=false;
 for(let i=0;i<50;i++){try{const {r,value}=await post(init);assert.equal(r.status,200);assert(value.result.serverInfo);ready=true;break;}catch{await new Promise(r=>setTimeout(r,100));}}
 assert(ready,'HTTP initialize failed');
 checkTools((await post(list)).value);checkAuth((await post(auth)).value);
 assert.equal((await post(list,{Authorization:''})).r.status,401);
 assert.equal((await post(list,{Origin:'https://evil.example'})).r.status,403);
 assert.equal(execFileSync('curl',['-s','-o','/dev/null','-w','%{http_code}','-H','Host: evil.example','http://127.0.0.1:18080/mcp'],{encoding:'utf8'}),'403');
 assert.equal((await post({...list,params:{padding:'x'.repeat(1100000)}})).r.status,413);
 console.log('PASS HTTP initialize + tools/list + auth_status; missing auth=401, hostile Origin/Host=403, oversized body=413');
} finally {execFileSync('docker',['stop',name]);}
console.log('Image bytes:',execFileSync('docker',['image','inspect',image,'--format','{{.Size}}'],{encoding:'utf8'}).trim());
