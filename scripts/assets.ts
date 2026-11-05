import { mkdir, readFile, writeFile, copyFile } from "node:fs/promises"
import { resolve, dirname } from "node:path"

const root = resolve(import.meta.dir, "..")
const web = resolve(root, "../web")
const source = await readFile(resolve(web, "src/i18n.ts"), "utf8")
const dictionaries = source.slice(0, source.indexOf("const dictionaries:")).replace(/^import .*\n/gm, "")
const javascript = new Bun.Transpiler({ loader: "ts" }).transformSync(`${dictionaries}\nexport default { en, 'zh-CN': zhCN, ja }`)
const module = await import(`data:text/javascript;base64,${Buffer.from(javascript).toString("base64")}`)
function flatten(value: any, prefix = "", result: Record<string, string> = {}) {
  for (const [key, entry] of Object.entries(value)) {
    const name = prefix ? `${prefix}.${key}` : key
    if (typeof entry === "function") result[name] = entry(...Array.from({ length: 8 }, (_, i) => `%[${i + 1}]v`))
    else if (typeof entry === "object") flatten(entry, name, result)
    else result[name] = String(entry)
  }
  return result
}
await mkdir(resolve(root, "assets/icons"), { recursive: true })
await mkdir(resolve(root, "assets/locales"), { recursive: true })
await mkdir(resolve(root, "assets/models"), { recursive: true })
for (const [locale, dictionary] of Object.entries(module.default)) {
  await writeFile(resolve(root, `assets/locales/${locale}.json`), `${JSON.stringify(flatten(dictionary), null, 2)}\n`)
}
await copyFile(resolve(web, "public/icon.svg"), resolve(root, "assets/icon.svg"))
await copyFile(resolve(web, "public/pwa-512x512.png"), resolve(root, "assets/icon.png"))
const logo = await readFile(resolve(web, "src/components/ui/FsvrLogo.tsx"), "utf8")
await writeFile(resolve(root, "assets/logo.svg"), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 320">${logo.slice(logo.indexOf("<defs>"), logo.indexOf("</svg>"))}</svg>\n`)
const iconSource = await readFile(resolve(web, "src/components/ui/Icon.tsx"), "utf8")
const names = [...iconSource.matchAll(/"([^"]+)": "i-ph-([^"]+)"/g)]
const iconify = JSON.parse(await readFile(resolve(web,"node_modules/@iconify-json/ph/icons.json"),"utf8"))
for (const [, name, id] of names) {
  const data = iconify.icons[id]
  if (!data) throw new Error(`Missing icon ${id}`)
  await writeFile(resolve(root, `assets/icons/${name}.svg`), `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${data.width ?? iconify.width} ${data.height ?? iconify.height}">${data.body}</svg>\n`)
}
for (const range of ["full", "short"]) await copyFile(resolve(web, `public/models/face_detector/blaze_face_${range}_range.tflite`), resolve(root, `assets/models/blaze_face_${range}_range.tflite`))
const projections=await readFile(resolve(web,"src/components/ui/ProjectionIcon.tsx"),"utf8")
for(const [,id,body] of projections.matchAll(/case "([^"]+)":\s*return \(\s*<>\s*([\s\S]*?)\s*<\/>\s*\)/g)) {
  await writeFile(resolve(root,`assets/icons/projection-${id}.svg`),`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">${body}</svg>\n`)
}
if(process.platform==="darwin") {
  const result=await Bun.spawn(["sips","-s","format","png",resolve(root,"assets/logo.svg"),"--out",resolve(root,"assets/logo.png")],{stdout:"ignore",stderr:"inherit"}).exited
  if(result!==0)throw new Error("Logo rasterization failed")
}
