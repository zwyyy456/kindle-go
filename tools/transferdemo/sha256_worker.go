package main

// Incremental SHA-256 worker. WebCrypto digest() is deliberately not used:
// it is one-shot and would require retaining the whole file in memory.
const sha256WorkerJS = `
"use strict";
var h, buffer, bytes, blocks;
var K = [
  0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,
  0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,
  0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,
  0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,
  0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,
  0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,
  0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,
  0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2
];
function reset() {
  h=[0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19];
  buffer=[]; bytes=0; blocks=0;
}
function rotr(x,n){return (x>>>n)|(x<<(32-n));}
function compress(chunk) {
  var w=new Array(64), i;
  for(i=0;i<16;i++) {
    var j=i*4;
    w[i]=((chunk[j]<<24)|(chunk[j+1]<<16)|(chunk[j+2]<<8)|chunk[j+3])|0;
  }
  for(i=16;i<64;i++) {
    var x=w[i-15], y=w[i-2];
    var s0=rotr(x,7)^rotr(x,18)^(x>>>3);
    var s1=rotr(y,17)^rotr(y,19)^(y>>>10);
    w[i]=(w[i-16]+s0+w[i-7]+s1)|0;
  }
  var a=h[0],b=h[1],c=h[2],d=h[3],e=h[4],f=h[5],g=h[6],hh=h[7];
  for(i=0;i<64;i++) {
    var S1=rotr(e,6)^rotr(e,11)^rotr(e,25);
    var ch=(e&f)^((~e)&g);
    var t1=(hh+S1+ch+K[i]+w[i])|0;
    var S0=rotr(a,2)^rotr(a,13)^rotr(a,22);
    var maj=(a&b)^(a&c)^(b&c);
    var t2=(S0+maj)|0;
    hh=g;g=f;f=e;e=(d+t1)|0;d=c;c=b;b=a;a=(t1+t2)|0;
  }
  h[0]=(h[0]+a)|0;h[1]=(h[1]+b)|0;h[2]=(h[2]+c)|0;h[3]=(h[3]+d)|0;
  h[4]=(h[4]+e)|0;h[5]=(h[5]+f)|0;h[6]=(h[6]+g)|0;h[7]=(h[7]+hh)|0;
  blocks++;
}
function update(input) {
  var data=new Uint8Array(input), i=0;
  bytes += data.length;
  while(i<data.length) {
    buffer.push(data[i++]);
    if(buffer.length===64){compress(buffer);buffer=[];}
  }
}
function finish() {
  var bitHigh=Math.floor(bytes/0x20000000);
  var bitLow=(bytes<<3)>>>0;
  buffer.push(0x80);
  while((buffer.length%64)!==56) buffer.push(0);
  buffer.push((bitHigh>>>24)&255,(bitHigh>>>16)&255,(bitHigh>>>8)&255,bitHigh&255);
  buffer.push((bitLow>>>24)&255,(bitLow>>>16)&255,(bitLow>>>8)&255,bitLow&255);
  while(buffer.length){compress(buffer.slice(0,64));buffer=buffer.slice(64);}
  var out="";
  for(var i=0;i<8;i++) out+=("00000000"+(h[i]>>>0).toString(16)).slice(-8);
  return out;
}
reset();
self.onmessage=function(event){
  var msg=event.data||{};
  if(msg.kind==="reset"){reset();self.postMessage({kind:"reset"});}
  else if(msg.kind==="chunk"){update(msg.buffer);self.postMessage({kind:"updated",bytes:bytes});}
  else if(msg.kind==="finish"){self.postMessage({kind:"digest",sha256:finish(),bytes:bytes,blocks:blocks});reset();}
};`
