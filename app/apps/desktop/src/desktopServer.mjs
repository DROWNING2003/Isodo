import http from "node:http";
import https from "node:https";
import fs from "node:fs";
import path from "node:path";
import { once } from "node:events";

export function parseRemoteAPI(value) {
  if (!value) throw new Error("Set ISODO_REMOTE_API_URL or desktop.json remoteApiUrl");
  const url = new URL(value);
  if (url.username || url.password || url.search || url.hash || !["", "/"].includes(url.pathname)) throw new Error("Remote API must be a plain origin");
  if (url.protocol !== "https:" && !(url.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(url.hostname))) throw new Error("Remote API must be HTTPS except loopback SSH tunnels");
  return url;
}
const mime = {".html":"text/html; charset=utf-8",".js":"text/javascript; charset=utf-8",".css":"text/css; charset=utf-8",".json":"application/json",".svg":"image/svg+xml",".png":"image/png",".woff2":"font/woff2",".wasm":"application/wasm"};
const blocked = new Set(["host","connection","proxy-connection","upgrade","keep-alive","te","trailer","transfer-encoding","proxy-authorization","x-forwarded-for","x-forwarded-host","forwarded"]);
function headersOf(headers, host) {
  const result = {};
  for (const [key,value] of Object.entries(headers)) if (!blocked.has(key.toLowerCase())) result[key]=value;
  result.host=host;
  return result;
}
function reject(res,status,message) { res.writeHead(status,{"Content-Type":"text/plain","Cache-Control":"no-store"});res.end(message); }
export async function startDesktopServer({staticDir,apiURL,port=5734}) {
  const remote=parseRemoteAPI(apiURL);
  const root=fs.realpathSync(staticDir);
  if(!fs.existsSync(path.join(root,"index.html"))) throw new Error("Desktop web/dist/index.html missing");
  const expectedHost="127.0.0.1:"+port;
  const transport=remote.protocol==="https:"?https:http;
  const server=http.createServer((req,res)=>{
    if(req.headers.host!==expectedHost) return reject(res,403,"Invalid Host");
    if(!req.url?.startsWith("/")||req.url.startsWith("//")) return reject(res,400,"Invalid URL");
    let url;
    try { url=new URL(req.url,"http://"+expectedHost); } catch { return reject(res,400,"Invalid URL"); }
    if(url.pathname.startsWith("/v1/")||url.pathname.startsWith("/auth/")||url.pathname==="/health"){
      const upstream=transport.request(remote,{method:req.method,path:url.pathname+url.search,headers:headersOf(req.headers,remote.host)},reply=>{
        res.writeHead(reply.statusCode??502,reply.headers);
        reply.pipe(res);
      });
      upstream.on("error",()=>{if(!res.headersSent)reject(res,502,"Remote API unavailable");else res.destroy();});
      req.pipe(upstream);
      return;
    }
    if(req.method!=="GET"&&req.method!=="HEAD")return reject(res,405,"Method not allowed");
    let pathname;
    try{pathname=decodeURIComponent(url.pathname);}catch{return reject(res,400,"Invalid URL");}
    if(pathname.includes("\\")||pathname.includes("\0")||pathname.split("/").includes(".."))return reject(res,400,"Invalid URL");
    const target=path.resolve(root,"."+pathname);
    if(target!==root&&!target.startsWith(root+path.sep))return reject(res,403,"Forbidden");
    let selected=target;
    try {if(!fs.statSync(selected).isFile())selected=path.join(root,"index.html");}catch{selected=path.join(root,"index.html");}
    const ext=path.extname(selected);
    res.writeHead(200,{"Content-Type":mime[ext]??"application/octet-stream","Cache-Control":ext===".html"?"no-store":"private, max-age=3600","X-Content-Type-Options":"nosniff"});
    fs.createReadStream(selected).pipe(res);
  });
  server.on("upgrade",(req,socket,head)=>{
    if(req.headers.host!==expectedHost||!req.url?.startsWith("/"))return socket.destroy();
    const url=new URL(req.url,"http://"+expectedHost);
    if(!["/v1/","/ws","/api/"].some(p=>url.pathname.startsWith(p)))return socket.destroy();
    const headers=headersOf(req.headers,remote.host);
    headers.connection="Upgrade";headers.upgrade="websocket";
    const upstream=transport.request(remote,{method:req.method,path:url.pathname+url.search,headers});
    upstream.on("upgrade",(reply,peer,initial)=>{
      socket.write("HTTP/1.1 101 Switching Protocols\r\n"+Object.entries(reply.headers).map(([k,v])=>k+": "+v).join("\r\n")+"\r\n\r\n");
      if(head.length)peer.write(head);
      if(initial.length)socket.write(initial);
      socket.pipe(peer).pipe(socket);
      peer.on("error",()=>socket.destroy());
    });
    upstream.on("error",()=>socket.destroy());
    upstream.on("response",()=>socket.destroy());
    upstream.end();
  });
  server.listen(port,"127.0.0.1");
  await once(server,"listening");
  return {url:"http://"+expectedHost,close:()=>new Promise(resolve=>server.close(resolve))};
}
