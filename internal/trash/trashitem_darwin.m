#import <Foundation/Foundation.h>
#include <stdlib.h>

#include "trashitem_darwin.h"

char *recyclerTrashItem(const char *path, char **out) {
	@autoreleasepool {
		NSString *p = [NSString stringWithUTF8String:path];
		NSURL *url = [NSURL fileURLWithPath:p];
		NSURL *landed = nil;
		NSError *err = nil;
		if (![[NSFileManager defaultManager] trashItemAtURL:url
		                                   resultingItemURL:&landed
		                                              error:&err]) {
			return strdup([[err localizedDescription] UTF8String]);
		}
		*out = strdup([[landed path] UTF8String]);
		return NULL;
	}
}
