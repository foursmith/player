// Run with `just preview-dmg`; packaging uses the same artwork.
import AppKit

struct Manifest: Decodable { let name: String }
let manifest = try JSONDecoder().decode(Manifest.self, from: Data(contentsOf: URL(fileURLWithPath: "mygo.json")))
guard CommandLine.arguments.count == 3 else {
    fatalError("Usage: swift scripts/dmg-artwork.swift <artwork-directory> <preview.png>")
}
let directory = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
let preview = URL(fileURLWithPath: CommandLine.arguments[2])
try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
try FileManager.default.createDirectory(at: preview.deletingLastPathComponent(), withIntermediateDirectories: true)

// Keep these dimensions and icon centers in sync with dmg-layout.py.
let width = 600, height = 400
let iconSize: CGFloat = 96
let appCenter = CGPoint(x: 172, y: 220)
let applicationsCenter = CGPoint(x: 428, y: 220)
let ink = NSColor(srgbRed: 0.96, green: 1, blue: 0.99, alpha: 0.88)
let muted = NSColor(srgbRed: 0.96, green: 1, blue: 0.99, alpha: 0.48)

func text(_ value: String, center: CGFloat, top: CGFloat, size: CGFloat, color: NSColor,
          weight: NSFont.Weight = .regular, tracking: CGFloat = 0, italic: Bool = false) {
    let font = NSFont.systemFont(ofSize: size, weight: weight)
    let attributes: [NSAttributedString.Key: Any] = [
        .font: italic ? NSFontManager.shared.convert(font, toHaveTrait: .italicFontMask) : font,
        .foregroundColor: color, .kern: tracking,
    ]
    let label = NSAttributedString(string: value, attributes: attributes)
    label.draw(at: CGPoint(x: center - label.size().width / 2, y: top))
}

// Static time-zero frame of the app's ocean shader in empty.go.
// Reuse its colors, noise, light shafts, caustics and vignette.
func smooth(_ a: Double, _ b: Double, _ x: Double) -> Double {
    let t = min(1, max(0, (x - a) / (b - a)))
    return t * t * (3 - 2 * t)
}
func mix(_ a: Double, _ b: Double, _ t: Double) -> Double { a + (b - a) * t }
func hash(_ x: Double, _ y: Double) -> Double {
    let n = sin(x * 127.1 + y * 311.7) * 43758.5453123
    return n - floor(n)
}
func noise(_ x: Double, _ y: Double) -> Double {
    let ix = floor(x), iy = floor(y)
    let fx = x - ix, fy = y - iy
    let ux = fx * fx * (3 - 2 * fx), uy = fy * fy * (3 - 2 * fy)
    return mix(mix(hash(ix, iy), hash(ix + 1, iy), ux), mix(hash(ix, iy + 1), hash(ix + 1, iy + 1), ux), uy)
}
func fbm(_ x: Double, _ y: Double) -> Double {
    var x = x, y = y, value = 0.0, amplitude = 0.5
    for _ in 0..<3 {
        value += amplitude * noise(x, y)
        x = x * 2.03 + 17.1; y = y * 2.03 + 9.2; amplitude *= 0.5
    }
    return value
}
func oceanFrame() -> NSImage {
    let w = width * 2, h = height * 2
    let shallow = [0.025, 0.285, 0.390], deep = [0.012, 0.105, 0.165]
    let surface = [0.160, 0.650, 0.720], texture = [0.018, 0.055, 0.065]
    let light = [0.38, 0.81, 0.84], causticColor = [0.72, 0.95, 0.93]
    var pixels = [UInt8](repeating: 255, count: w * h * 4)
    for y in 0..<h {
        let v = 1 - (Double(y) + 0.5) / Double(h)
        let depth = smooth(0, 1, v)
        let depthMix = pow(depth, 1.45)
        let surfaceMix = pow(depth, 5) * 0.48
        let causticDepth = pow(depth, 6)
        let rayFade = smooth(0.02, 0.75, v) * smooth(1.12, 0.62, v)
        for x in 0..<w {
            let u = (Double(x) + 0.5) / Double(w), wx = (u - 0.5) * 1.5
            let current = fbm(wx * 2.2, v * 2.8)
            let fine = noise(wx * 5.5, v * 4)
            var rays = 0.0
            for i in 0..<3 {
                let f = Double(i)
                let origin = 0.16 + f * 0.31 + sin(f * 2.1) * 0.025
                let center = origin + (0.14 + (f - 1) * 0.055) * (1 - v)
                rays += exp(-abs(u - center) * (13 + f * 2.7)) * (0.7 + 0.3 * current)
            }
            rays *= rayFade
            let waveA = abs(sin(wx * 8 + sin(v * 6 * 0.74)))
            let waveB = abs(sin(v * 6 * 1.17 + sin(wx * 8 * 0.63)))
            let caustic = pow(max(0, 1 - min(waveA, waveB)), 5) * causticDepth * (0.45 + fine * 0.55)
            let vignette = 0.7 + (1 - smooth(0.28, 0.82, hypot(u - 0.5, v - 0.54))) * 0.3
            for channel in 0..<3 {
                var c = mix(deep[channel], shallow[channel], depthMix)
                c = mix(c, surface[channel], surfaceMix)
                c += (current - 0.5) * texture[channel] + light[channel] * rays * 0.14 + causticColor[channel] * caustic * 0.16
                pixels[(y * w + x) * 4 + channel] = UInt8(min(255, max(0, c * vignette * 255)))
            }
        }
    }
    let provider = CGDataProvider(data: Data(pixels) as CFData)!
    let image = CGImage(width: w, height: h, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: w * 4,
                        space: CGColorSpace(name: CGColorSpace.sRGB)!, bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.last.rawValue),
                        provider: provider, decode: nil, shouldInterpolate: true, intent: .defaultIntent)!
    return NSImage(cgImage: image, size: NSSize(width: width, height: height))
}
let ocean = oceanFrame()

func drawBackground() {
    ocean.draw(in: CGRect(x: 0, y: 0, width: width, height: height), from: .zero,
               operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
    text(manifest.name, center: 300, top: 62, size: 22, color: ink, weight: .medium, tracking: -0.3)
    text("Elegant video player, especially for VR.", center: 300, top: 98, size: 10.5, color: muted, tracking: 1.2, italic: true)
    NSColor(srgbRed: 0.72, green: 0.95, blue: 0.93, alpha: 0.42).setStroke()
    let arrow = NSBezierPath()
    arrow.lineWidth = 1.5
    arrow.lineCapStyle = .round
    arrow.lineJoinStyle = .round
    arrow.move(to: CGPoint(x: 281, y: 220)); arrow.line(to: CGPoint(x: 319, y: 220))
    arrow.move(to: CGPoint(x: 310, y: 211)); arrow.line(to: CGPoint(x: 319, y: 220)); arrow.line(to: CGPoint(x: 310, y: 229))
    arrow.stroke()
    // Keep the picker's translucent treatment, with enough light for Finder's black text.
    for center in [appCenter, applicationsCenter] {
        let rect = CGRect(x: center.x - 70, y: 272, width: 140, height: 27)
        let plate = NSBezierPath(roundedRect: rect, xRadius: 13.5, yRadius: 13.5)
        NSColor.white.withAlphaComponent(0.32).setFill()
        plate.fill()
        let x = rect.minX + 0.5, y = rect.minY + 0.5, w = rect.width - 1, r: CGFloat = 13
        let k = r * 0.55228475
        let rim = CGMutablePath()
        rim.move(to: CGPoint(x: x, y: y + r))
        rim.addCurve(to: CGPoint(x: x + r, y: y), control1: CGPoint(x: x, y: y + r - k),
                     control2: CGPoint(x: x + r - k, y: y))
        rim.addLine(to: CGPoint(x: x + w - r, y: y))
        rim.addCurve(to: CGPoint(x: x + w, y: y + r), control1: CGPoint(x: x + w - r + k, y: y),
                     control2: CGPoint(x: x + w, y: y + r - k))
        let context = NSGraphicsContext.current!.cgContext
        context.saveGState()
        context.addPath(rim)
        context.setLineWidth(0.75)
        context.replacePathWithStrokedPath()
        context.clip()
        let alphas: [CGFloat] = [0, 0.14 * 8 / 15, 0.14, 0.14 * 8 / 15, 0]
        let colors = alphas.map {
            NSColor.white.withAlphaComponent($0).cgColor
        }
        let reflection = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB)!,
                                    colors: colors as CFArray, locations: [0, 0.25, 0.5, 0.75, 1])!
        context.drawLinearGradient(reflection, start: CGPoint(x: rect.minX, y: y),
                                   end: CGPoint(x: rect.maxX, y: y), options: [])
        context.restoreGState()
    }
    text("Drag to Applications to install", center: 300, top: 337, size: 11, color: muted, tracking: 0.4)
}

func render(scale: Int, icons: Bool, target: URL) throws {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: width * scale, pixelsHigh: height * scale,
                                  bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                                  colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let graphics = NSGraphicsContext(bitmapImageRep: bitmap)!
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = graphics
    let context = graphics.cgContext
    context.translateBy(x: 0, y: CGFloat(height * scale))
    context.scaleBy(x: CGFloat(scale), y: -CGFloat(scale))
    NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: true)
    drawBackground()
    if icons {
        let app = NSImage(contentsOfFile: "assets/icon.png")!
        let folder = NSWorkspace.shared.icon(forFile: "/Applications")
        for (image, center) in [(app, appCenter), (folder, applicationsCenter)] {
            image.draw(in: CGRect(x: center.x - iconSize / 2, y: center.y - iconSize / 2, width: iconSize, height: iconSize),
                       from: .zero, operation: .sourceOver, fraction: 1, respectFlipped: true, hints: nil)
        }
        let filenameColor = NSColor.black.withAlphaComponent(0.9)
        text(manifest.name, center: appCenter.x, top: 278, size: 13, color: filenameColor)
        text("Applications", center: applicationsCenter.x, top: 278, size: 13, color: filenameColor)
    }
    NSGraphicsContext.restoreGraphicsState()
    bitmap.size = NSSize(width: width, height: height)
    try bitmap.representation(using: .png, properties: [:])!.write(to: target)
}

try render(scale: 1, icons: false, target: directory.appendingPathComponent("background.png"))
try render(scale: 2, icons: false, target: directory.appendingPathComponent("background@2x.png"))
try render(scale: 2, icons: true, target: preview)
print("DMG preview: \(preview.path)")
