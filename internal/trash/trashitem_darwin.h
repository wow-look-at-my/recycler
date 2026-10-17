#ifndef RECYCLER_TRASHITEM_H
#define RECYCLER_TRASHITEM_H

// Moves the item at path to the trash, writing where it landed to *out.
// Answers a message on failure and NULL on success; both are the caller's
// to free.
char *recyclerTrashItem(const char *path, char **out);

#endif
