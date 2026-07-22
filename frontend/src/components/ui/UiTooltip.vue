<script lang="ts">
import { cloneVNode, defineComponent } from 'vue'

export default defineComponent({
  name: 'UiTooltip',
  inheritAttrs: false,
  props: {
    text: { type: String, required: true },
    side: { type: String, default: 'bottom' },
  },
  setup(props, { attrs, slots }) {
    return () => {
      const children = slots.default?.() || []
      if (!children.length) return null
      if (children.length > 1) return children
      return cloneVNode(children[0], { ...attrs, title: props.text })
    }
  },
})
</script>
