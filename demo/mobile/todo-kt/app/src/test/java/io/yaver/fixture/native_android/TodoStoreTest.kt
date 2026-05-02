package io.yaver.fixture.native_android

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class TodoStoreTest {
  @Test
  fun addTrimsAndPrependsItem() {
    val store = TodoStore()
    val initialSize = store.items.size

    val added = store.add("  Validate relay fallback  ")

    assertTrue(added)
    assertEquals(initialSize + 1, store.items.size)
    assertEquals("Validate relay fallback", store.items.first().title)
  }

  @Test
  fun toggleFlipsDoneFlag() {
    val store = TodoStore()
    val original = store.items.first()

    store.toggle(original.id)

    assertEquals(!original.done, store.items.first().done)
  }

  @Test
  fun removeDeletesItem() {
    val store = TodoStore()
    val item = store.items[1]

    store.remove(item.id)

    assertFalse(store.items.any { it.id == item.id })
  }
}
