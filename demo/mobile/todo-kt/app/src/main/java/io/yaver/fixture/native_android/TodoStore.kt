package io.yaver.fixture.native_android

class TodoStore {
  private var nextId = 4

  private val _items = mutableListOf(
    TodoItem(1, "Review remote runtime"),
    TodoItem(2, "Test tap controls"),
    TodoItem(3, "Trigger feedback overlay", done = true),
  )

  val items: List<TodoItem>
    get() = _items.toList()

  fun add(title: String): Boolean {
    val trimmed = title.trim()
    if (trimmed.isEmpty()) return false
    _items.add(0, TodoItem(nextId++, trimmed))
    return true
  }

  fun toggle(id: Int) {
    val index = _items.indexOfFirst { it.id == id }
    if (index >= 0) {
      val item = _items[index]
      _items[index] = item.copy(done = !item.done)
    }
  }

  fun remove(id: Int) {
    _items.removeAll { it.id == id }
  }
}
