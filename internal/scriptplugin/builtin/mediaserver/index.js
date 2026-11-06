// A minimal folder-based Jellyfin/Emby source. Decoding stays in the player.
const fail=(code,message)=>Object.assign(new Error(message),{code});
function address(config,path){return config.server+"/"+path.replace(/^\//,"");}
function headers(credentials){
 const result={"X-Emby-Authorization":`MediaBrowser Client="Foursmith Player", Device="Desktop", DeviceId="${host.sourceId}", Version="1.0"`};
 if(credentials.token)result["X-Emby-Token"]=credentials.token;
 return result;
}
async function request(args,path,body,method){
 const h=headers(args.credentials||{});if(body!==undefined)h["Content-Type"]="application/json";
 const response=await fetch(address(args.config,path),{method:method||(body===undefined?"GET":"POST"),headers:h,body:body===undefined?undefined:JSON.stringify(body),signal:args.signal});
 if(response.status===401||response.status===403)throw fail("signIn","The server requires a valid login or access token.");
 if(!response.ok)throw fail("unavailable",`Server returned HTTP ${response.status}.`);
 if(response.status===204)return null;
 const text=await response.text();return text?JSON.parse(text):null;
}
export async function authenticate(args){
 const c=args.credentials||{};
 if(c.password){
  const result=await request({...args,credentials:{}},"Users/AuthenticateByName",{Username:args.config.username||"",Pw:c.password});
  if(!result?.AccessToken||!result?.User?.Id)throw fail("signIn","The server did not return an access token.");
  return {token:result.AccessToken,userId:result.User.Id,accountId:result.User.Id};
 }
 if(c.token){const user=await request(args,"Users/Me");return {token:c.token,userId:user.Id,accountId:user.Id};}
 throw fail("setup","Enter a password or access token.");
}
function signed(args,path){return {url:address(args.config,path),headers:headers(args.credentials)};}
export async function browse(args){
 const c=args.credentials||{};if(!c.token||!c.userId)throw fail("signIn","Connect to the server first.");
 const limit=Math.max(1,Math.min(args.limit||200,200));const start=Number(args.cursor||0);
 if(!Number.isSafeInteger(start)||start<0)throw fail("unavailable","Invalid page cursor.");
 const query=new URLSearchParams({ParentId:args.parentId||"",StartIndex:String(start),Limit:String(limit),Fields:"MediaSources,DateCreated",SortBy:"SortName",SortOrder:"Ascending",Recursive:"false"});
 const result=await request(args,`Users/${encodeURIComponent(c.userId)}/Items?${query}`);
 const rows=result.Items||[];
 return {items:rows.filter(x=>x.IsFolder||x.MediaType==="Video"||["Movie","Episode","Video","TvChannel","LiveTvChannel"].includes(x.Type)).map(x=>({
  itemId:x.Id,name:x.Name,folder:!!x.IsFolder,duration:Number(x.RunTimeTicks||0)/1e7,
  size:Number(x.MediaSources?.[0]?.Size||0),modified:Date.parse(x.DateCreated||"")||0
 })),nextCursor:start+rows.length<Number(result.TotalRecordCount||0)&&rows.length?String(start+rows.length):""};
}
async function playback(args,playing){
 if(!args.credentials?.token)throw fail("signIn","Connect to the server first.");
 const result=await request(args,`Items/${encodeURIComponent(args.id)}/PlaybackInfo`,{
  UserId:args.credentials.userId,IsPlayback:playing,EnableDirectPlay:true,EnableDirectStream:true,EnableTranscoding:false,
  DeviceProfile:{Name:"Foursmith Player",MaxStreamingBitrate:2000000000,DirectPlayProfiles:[{Type:"Video"}]},
  AutoOpenLiveStream:false,StartTimeTicks:Math.round((args.position||0)*1e7)
 });
 if(result.ErrorCode)throw fail("unavailable",String(result.ErrorCode));
 const media=(result.MediaSources||[]).find(x=>!x.RequiresOpening&&(x.SupportsDirectPlay!==false||x.SupportsDirectStream));
 if(!media)throw fail("unavailable","No directly playable source is available.");
 // Use server delivery instead of a backend filesystem path. It retains Range,
 // authentication and the same codec/container support as local playback.
 const q=new URLSearchParams({Static:"true",MediaSourceId:media.Id});
 const resource=signed(args,`Videos/${encodeURIComponent(args.id)}/stream?${q}`);
 const subtitle=(media.MediaStreams||[]).find(x=>x.Type==="Subtitle"&&x.IsExternal&&x.DeliveryUrl&&["srt","subrip","vtt","webvtt","ass","ssa"].includes(String(x.Codec).toLowerCase()));
 let sub;
 if(subtitle){const u=new URL(subtitle.DeliveryUrl,args.config.server+"/");sub={url:u.href,headers:headers(args.credentials)};}
 return {...resource,subtitle:sub,live:!!media.IsInfiniteStream,seekable:!media.IsInfiniteStream,
  session:playing?{playSessionId:result.PlaySessionId,mediaSourceId:media.Id,seekable:!media.IsInfiniteStream}:null};
}
export function resolve(args){return playback(args,false);}
export function prepare(args){return playback(args,true);}
export async function report(args){
 if(!args.session?.playSessionId)return;
 const e=args.event;
 const body={ItemId:args.id,PlaySessionId:args.session.playSessionId,MediaSourceId:args.session.mediaSourceId,
  PositionTicks:Math.round((e.position||0)*1e7),IsPaused:e.paused,CanSeek:!!args.session.seekable,PlayMethod:"DirectPlay",Failed:e.failed};
 const suffix=e.type==="start"?"":e.type==="stop"?"/Stopped":"/Progress";
 await request(args,"Sessions/Playing"+suffix,body);
}
