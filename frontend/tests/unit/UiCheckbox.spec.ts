import { mount } from '@vue/test-utils'
import UiCheckbox from '../../src/components/ui/UiCheckbox.vue'

describe('UiCheckbox', () => {
  it('切换时发出布尔值更新', async () => {
    const wrapper = mount(UiCheckbox, {
      props: { modelValue: false, ariaLabel: '选择图片' },
    })

    await wrapper.get('[role="checkbox"]').trigger('click')

    expect(wrapper.emitted('update:modelValue')).toEqual([[true]])
  })
})
