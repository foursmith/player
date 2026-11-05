package exporter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
)

func MotionPhoto(cover, video []byte) ([]byte, error) {
	if len(cover) < 2 || cover[0] != 0xff || cover[1] != 0xd8 {
		return nil, fmt.Errorf("invalid JPEG cover")
	}
	xmp := fmt.Sprintf(`http://ns.adobe.com/xap/1.0/%c<?xpacket begin="BOM" id="W5M0MpCehiHzreSzNTczkc9d"?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#"><rdf:Description rdf:about="" xmlns:GCamera="http://ns.google.com/photos/1.0/camera/" xmlns:OpCamera="http://ns.oplus.com/photos/1.0/camera/" xmlns:MiCamera="http://ns.xiaomi.com/photos/1.0/camera/" xmlns:Container="http://ns.google.com/photos/1.0/container/" xmlns:Item="http://ns.google.com/photos/1.0/container/item/" GCamera:MotionPhoto="1" GCamera:MotionPhotoVersion="1" GCamera:MotionPhotoPresentationTimestampUs="0" GCamera:MicroVideo="1" GCamera:MicroVideoVersion="1" GCamera:MicroVideoOffset="%d" GCamera:MicroVideoPresentationTimestampUs="0" OpCamera:MotionPhotoPrimaryPresentationTimestampUs="0" OpCamera:MotionPhotoOwner="oplus" OpCamera:OLivePhotoVersion="2" OpCamera:VideoLength="%d" MiCamera:XMPMeta="&lt;?xml version='1.0' encoding='UTF-8' standalone='yes' ?&gt;"><Container:Directory><rdf:Seq><rdf:li rdf:parseType="Resource"><Container:Item Item:Mime="image/jpeg" Item:Semantic="Primary" Item:Length="0" Item:Padding="0"/></rdf:li><rdf:li rdf:parseType="Resource"><Container:Item Item:Mime="video/mp4" Item:Semantic="MotionPhoto" Item:Length="%d"/></rdf:li></rdf:Seq></Container:Directory></rdf:Description></rdf:RDF></x:xmpmeta><?xpacket end="w"?>`, 0, len(video), len(video), len(video))
	xmp = strings.ReplaceAll(xmp, "BOM", string(rune(65279)))
	if len(xmp)+2 > 65535 {
		return nil, fmt.Errorf("motion photo metadata too large")
	}
	insert := 2
	if len(cover) >= 6 && cover[2] == 0xff && cover[3] == 0xe0 {
		length := int(binary.BigEndian.Uint16(cover[4:6]))
		if length >= 2 && 4+length <= len(cover) {
			insert = 4 + length
		}
	}
	var out bytes.Buffer
	out.Grow(len(cover) + len(video) + len(xmp) + 4)
	out.Write(cover[:insert])
	out.Write([]byte{0xff, 0xe1, byte((len(xmp) + 2) >> 8), byte(len(xmp) + 2)})
	out.WriteString(xmp)
	out.Write(cover[insert:])
	out.Write(video)
	return out.Bytes(), nil
}
