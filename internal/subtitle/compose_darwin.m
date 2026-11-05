#import <Foundation/Foundation.h>
#import <CoreText/CoreText.h>
#import <CoreGraphics/CoreGraphics.h>
static CTLineRef makeLine(NSString *s,NSDictionary *attrs){return CTLineCreateWithAttributedString((__bridge CFAttributedStringRef)[[NSAttributedString alloc]initWithString:s attributes:attrs]);}
static double measure(NSString *s,NSDictionary *attrs){CTLineRef line=makeLine(s,attrs);double w=CTLineGetTypographicBounds(line,NULL,NULL,NULL);CFRelease(line);return w;}
int fsvr_subtitle(unsigned char *rgba,int width,int height,int stride,const char *utf8,double cssWidth){
 @autoreleasepool{
 if(cssWidth<=0)cssWidth=width;
 double scale=width/cssWidth,size=fmin(28,fmax(16,cssWidth*.022))*scale,lineHeight=size*1.38,maxWidth=fmin(cssWidth*.86,1152)*scale;
 CTFontRef normal=CTFontCreateUIFontForLanguage(kCTFontUIFontSystem,size,NULL);
 NSDictionary *traits=@{(__bridge NSString*)kCTFontWeightTrait:@(.23)};
 CTFontDescriptorRef descriptor=CTFontDescriptorCreateWithAttributes((__bridge CFDictionaryRef)@{(__bridge NSString*)kCTFontTraitsAttribute:traits});
 CTFontRef font=CTFontCreateCopyWithAttributes(normal,size,NULL,descriptor);CFRelease(normal);CFRelease(descriptor);
 NSDictionary *attrs=@{(__bridge NSString*)kCTFontAttributeName:(__bridge id)font,(__bridge NSString*)kCTForegroundColorFromContextAttributeName:@YES};
 NSString *text=[NSString stringWithUTF8String:utf8];NSMutableArray<NSString*> *lines=[NSMutableArray array];
 for(NSString *paragraph in [text componentsSeparatedByString:@"\n"]){
  if(paragraph.length==0){[lines addObject:@""];continue;}
  NSMutableArray<NSString*> *tokens=[NSMutableArray array];
  if([paragraph containsString:@" "]){NSRegularExpression *re=[NSRegularExpression regularExpressionWithPattern:@"\\s+|\\S+" options:0 error:NULL];for(NSTextCheckingResult *m in [re matchesInString:paragraph options:0 range:NSMakeRange(0,paragraph.length)])[tokens addObject:[paragraph substringWithRange:m.range]];}
  else{[paragraph enumerateSubstringsInRange:NSMakeRange(0,paragraph.length) options:NSStringEnumerationByComposedCharacterSequences usingBlock:^(NSString *s,NSRange a,NSRange b,BOOL *stop){[tokens addObject:s];}];}
  NSString *line=@"";NSCharacterSet *spaces=[NSCharacterSet whitespaceCharacterSet];
  for(NSString *token in tokens){NSString *candidate=[line stringByAppendingString:token];if(line.length&&measure(candidate,attrs)>maxWidth){[lines addObject:[line stringByTrimmingCharactersInSet:spaces]];line=[token stringByTrimmingCharactersInSet:spaces];}else line=candidate;}
  if(line.length)[lines addObject:[line stringByTrimmingCharactersInSet:spaces]];
 }
 CGColorSpaceRef color=CGColorSpaceCreateDeviceRGB();CGContextRef context=CGBitmapContextCreate(rgba,width,height,8,stride,color,kCGImageAlphaPremultipliedLast|kCGBitmapByteOrder32Big);CGColorSpaceRelease(color);if(!context){CFRelease(font);return -1;}
 CGContextSetTextMatrix(context,CGAffineTransformIdentity);CGContextSetLineJoin(context,kCGLineJoinRound);CGContextSetLineWidth(context,fmax(2,3*scale));
 double top=height-height*.14-lines.count*lineHeight;
 for(NSUInteger i=0;i<lines.count;i++){CTLineRef line=makeLine(lines[i],attrs);double w=CTLineGetTypographicBounds(line,NULL,NULL,NULL),x=(width-w)/2,y=height-top-i*lineHeight-CTFontGetAscent(font);
  CGContextSetTextDrawingMode(context,kCGTextStroke);CGContextSetRGBStrokeColor(context,0,0,0,.82);CGContextSetTextPosition(context,x,y);CTLineDraw(line,context);
  CGContextSetTextDrawingMode(context,kCGTextFill);CGContextSetRGBFillColor(context,1,1,1,1);CGContextSetTextPosition(context,x,y);CTLineDraw(line,context);CFRelease(line);
 }
 CGContextRelease(context);CFRelease(font);return 0;
 }
}
