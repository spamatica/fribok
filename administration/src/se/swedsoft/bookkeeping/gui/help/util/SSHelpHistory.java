package se.swedsoft.bookkeeping.gui.help.util;

import javax.help.event.HelpModelEvent;
import javax.help.event.HelpModelListener;
import javax.help.JHelpContentViewer;
import javax.help.InvalidHelpSetContextException;
import java.util.List;
import java.util.LinkedList;
import java.awt.event.ActionListener;
import java.awt.event.ActionEvent;

/**
 * Date: 2006-mar-06
 * Time: 10:17:11
 */
public class SSHelpHistory {


    private JHelpContentViewer   iViewer;

    private List<HelpModelEvent> iHistory;

    private int                  iIndex;

    private boolean              iUpdating;

    private List<ActionListener> iListeners;


    /**
     *
     * @param pViewer
     */
    public SSHelpHistory(JHelpContentViewer pViewer){
        iViewer     = pViewer;
        iHistory    = new LinkedList<HelpModelEvent>();
        iIndex      = 0;
        iUpdating   = false;
        iListeners  = new LinkedList<ActionListener>();

        iViewer.addHelpModelListener(new HelpModelListener() {
            public void idChanged(HelpModelEvent helpModelEvent) {
                addHistoryEvent(helpModelEvent);
            }
        });
    }

    /**
     *
     * @param iListener
     */
    public void addHistoryListener(ActionListener iListener){
        iListeners.add(iListener);
    }

    /**
     *
     */
    private void notifyListeners(){
        ActionEvent iEvent =  new ActionEvent(this, 0, "");

        for(ActionListener iListener: iListeners){
            iListener.actionPerformed(iEvent);
        }
    }

    /**
     *
     * @param pEvent
     */
    private void addHistoryEvent(HelpModelEvent pEvent){
        if(iUpdating) return;

        iHistory = iHistory.subList(0, iIndex);

        iHistory.add(pEvent);

        iIndex = iHistory.size();

        notifyListeners();
    }

    /**
     *
     * @return
     */
    public boolean hasNext(){
        return getNext() != null;
    }

    /**
     *
     * @return
     */
    public boolean hasPrevious(){
        return getPrevious() != null;
    }


    /**
     *
     */
    public void back(){
        iIndex--;

        navigate();
    }

    /**
     *
     */
    public void forward(){
        iIndex++;

        navigate();
    }


    /**
     *
     */
    private void navigate(){
        setUpdating(true);

        HelpModelEvent iEvent = getCurrent();

        try {

            if(iEvent != null) {
                iViewer.setCurrentID( iEvent.getID() );
                notifyListeners();
            }

        } catch (InvalidHelpSetContextException e) {
            e.printStackTrace();
        }


        setUpdating(false);
    }

    /**
     *
     * @param pUpdating
     */
    private void setUpdating(boolean pUpdating){
        iUpdating = pUpdating;
    }


    /**
     *
     * @return
     */
    private HelpModelEvent getCurrent(){
        int theIndex = iIndex-1;

        if(theIndex > 0 && theIndex < iHistory.size() ) {
            return iHistory.get(theIndex);
        }
        return null;
    }

    /**
     *
     * @return
     */
    private HelpModelEvent getNext(){
        int theIndex = iIndex;

        if(theIndex > 0 && theIndex < iHistory.size() ) {
            return iHistory.get(theIndex);
        }
        return null;
    }

    /**
     *
     * @return
     */
    private HelpModelEvent getPrevious(){
        int theIndex = iIndex-2;

        if(theIndex > 0 && theIndex < iHistory.size() ) {
            return iHistory.get(theIndex);
        }
        return null;
    }


    /**
     * Returns a string representation of the object. In general, the
     * <code>toString</code> method returns a string that
     * "textually represents" this object. The result should
     * be a concise but informative representation that is easy for a
     * person to read.
     *
     * @return a string representation of the object.
     */
    public String toString() {
        StringBuilder sb = new StringBuilder();

        int iCounter = 0;

        sb.append("SSHelpHistory {\n");
        for(HelpModelEvent iEvent: iHistory){
            if(iCounter == iIndex){
                sb.append(" *");
            }else{
                sb.append("  ");
            }
            sb.append(iEvent.getID());
            sb.append(", ");
            sb.append(iEvent.getHistoryName());
            sb.append("\n");

            iCounter++;
        }
        sb.append("}\n");

        return sb.toString();
    }


}
